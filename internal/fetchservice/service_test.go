package fetchservice

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls-rpc/internal/testsupport"
)

func TestBuildOptionsAppliesSharedPrecedence(t *testing.T) {
	options, err := BuildOptions(Params{
		URL:                 "https://example.test/",
		Proxy:               "http://proxy.invalid:8000",
		ParentProxy:         "http://parent.invalid:9000",
		UserAgent:           "contract-test/1.0",
		Browser:             "chrome160",
		HTTP2SettingsString: "1:65536;2:0;4:6291456;6:262144|15663105|0|m,a,s,p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Proxy != "http://parent.invalid:9000" {
		t.Fatalf("parent_proxy did not win: %q", options.Proxy)
	}
	if options.UserAgent != "contract-test/1.0" {
		t.Fatalf("explicit user agent did not win: %q", options.UserAgent)
	}
	if options.HTTP2SettingsString != "1:65536;2:0;4:6291456;6:262144|15663105|0|m,a,s,p" {
		t.Fatalf("HTTP/2 settings were not preserved: %q", options.HTTP2SettingsString)
	}
	if options.Timeout != defaultTimeoutSeconds {
		t.Fatalf("default timeout = %d", options.Timeout)
	}
}

func TestBuildOptionsRejectsInvalidContractValues(t *testing.T) {
	tests := []Params{
		{},
		{URL: "https://example.test", Timeout: -1},
		{URL: "https://example.test", FingerprintType: "unknown", FingerprintValue: "value"},
		{URL: "https://example.test", FingerprintType: "ja3"},
		{URL: "https://example.test", FingerprintSet: true},
		{URL: "https://example.test", HTTP2SettingsString: "not-settings"},
	}
	for _, params := range tests {
		if _, err := BuildOptions(params); err == nil {
			t.Fatalf("BuildOptions(%+v) unexpectedly succeeded", params)
		} else if _, ok := err.(*ConfigError); !ok {
			t.Fatalf("BuildOptions(%+v) error type = %T, want *ConfigError", params, err)
		}
	}
}

func TestFetchAgainstLocalTarget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			body, _ := io.ReadAll(r.Body)
			cookie, _ := r.Cookie("session")
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, strings.Join([]string{
				r.Method, string(body), r.Header.Get("X-Contract"), cookie.Value, r.UserAgent(),
			}, "|"))
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			writer := gzip.NewWriter(w)
			_, _ = writer.Write([]byte("compressed-response"))
			_ = writer.Close()
		case "/redirect":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			_, _ = io.WriteString(w, "redirected")
		case "/metadata":
			_, _ = io.ReadAll(r.Body)
			w.Header().Set("Trailer", "X-Response-Trailer")
			_, _ = io.WriteString(w, r.Trailer.Get("X-Request-Trailer"))
			w.Header().Set("X-Response-Trailer", "response-value")
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	result, err := Fetch(context.Background(), fastls.NewClient(), Params{
		URL: target.URL + "/echo", Method: http.MethodPost, Body: "payload",
		Headers:   map[string]string{"X-Contract": "shared"},
		Cookies:   []fastls.Cookie{{Name: "session", Value: "cookie-value"}},
		UserAgent: "contract-test/1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Status != http.StatusOK || result.Body != "POST|payload|shared|cookie-value|contract-test/1.0" {
		t.Fatalf("unexpected echo result: %+v", result)
	}

	result, err = Fetch(context.Background(), fastls.NewClient(), Params{URL: target.URL + "/gzip"})
	if err != nil || !result.OK || result.Body != "compressed-response" {
		t.Fatalf("unexpected gzip result: result=%+v err=%v", result, err)
	}

	result, err = Fetch(context.Background(), fastls.NewClient(), Params{
		URL: target.URL + "/redirect", DisableRedirect: true,
	})
	if err != nil || !result.OK || result.Status != http.StatusFound {
		t.Fatalf("redirect was not disabled: result=%+v err=%v", result, err)
	}

	result, err = Fetch(context.Background(), fastls.NewClient(), Params{
		URL: target.URL + "/metadata", Method: http.MethodPost, Body: "payload",
		Trailers: http.Header{"X-Request-Trailer": {"request-value"}},
	})
	if err != nil || !result.OK || result.Body != "request-value" || result.Protocol != "HTTP/1.1" ||
		result.FinalURL != target.URL+"/metadata" || result.Trailers.Get("X-Response-Trailer") != "response-value" {
		t.Fatalf("unexpected metadata result: result=%+v err=%v", result, err)
	}
}

func TestFetchParentProxyWins(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "parent-proxy")
	}))
	defer target.Close()
	proxy := testsupport.NewConnectProxy(t)

	result, err := Fetch(context.Background(), fastls.NewClient(), Params{
		URL:         target.URL + "/probe",
		Proxy:       "http://127.0.0.1:1",
		ParentProxy: proxy.URL(),
	})
	if err != nil || !result.OK || result.Body != "parent-proxy" || proxy.Connects() != 1 {
		t.Fatalf("parent proxy was not used: result=%+v err=%v", result, err)
	}
}

func TestFetchHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer target.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		result, err := Fetch(ctx, fastls.NewClient(), Params{URL: target.URL, Timeout: 5})
		if err != nil {
			result.Error = err.Error()
		}
		done <- result
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("downstream request did not start")
	}
	select {
	case result := <-done:
		if result.OK || !strings.Contains(strings.ToLower(result.Error), "context canceled") {
			t.Fatalf("unexpected cancellation result: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled request did not return")
	}
}
