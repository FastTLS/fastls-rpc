package fetchservice

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExecutorResponseContractLimitsAndRelease(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	binary := []byte{0, 255, 128, 1}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	writer.Write([]byte(strings.Repeat("x", 4096)))
	writer.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.Header().Add("X-Repeat", "one")
		w.Header().Add("X-Repeat", "two")
		w.Header().Set("Trailer", "X-Final")
		if r.URL.Path == "/gzip" {
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(compressed.Bytes())
		} else if r.URL.Path == "/broken" {
			w.Header().Set("Content-Encoding", "gzip")
			io.WriteString(w, "invalid")
		} else {
			w.Write(binary)
		}
		w.Header().Set("X-Final", "complete")
	}))
	defer target.Close()
	defer close(release)
	for _, test := range []struct {
		path    string
		maximum int64
		code    string
	}{{"/binary", 4, ""}, {"/binary", 3, "response_too_large"}, {"/gzip", 128, "response_too_large"}, {"/broken", 0, "decode_error"}} {
		executor, err := NewExecutor(Limits{MaxResponseBytes: test.maximum})
		if err != nil {
			t.Fatal(err)
		}
		result, err := executor.Fetch(context.Background(), Params{URL: target.URL + test.path, IncludeBodyBase64: true})
		if err != nil {
			t.Fatal(err)
		}
		if result.ErrorCode != test.code || result.OK != (test.code == "") {
			t.Fatalf("%+v => %+v", test, result)
		}
		if result.OK {
			body, err := base64.StdEncoding.DecodeString(result.BodyBase64)
			if err != nil || !bytes.Equal(body, binary) {
				t.Fatal("wire bytes changed")
			}
			if !reflect.DeepEqual(result.HeaderValues.Values("X-Repeat"), []string{"one", "two"}) || len(result.HeaderValues.Values("Set-Cookie")) != 2 || result.Trailers.Get("X-Final") != "complete" {
				t.Fatalf("metadata lost: %+v", result)
			}
		}
	}
	executor, err := NewExecutor(Limits{MaxInflight: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() { result, _ := executor.Fetch(ctx, Params{URL: target.URL + "/hold"}); done <- result }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not arrive")
	}
	result, err := executor.Fetch(context.Background(), Params{URL: target.URL})
	if err != nil || result.ErrorCode != "overloaded" {
		t.Fatalf("overload = %+v %v", result, err)
	}
	cancel()
	select {
	case result = <-done:
		if result.ErrorCode != "cancelled" {
			t.Fatalf("cancel = %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled call retained slot")
	}
	result, err = executor.Fetch(context.Background(), Params{URL: target.URL})
	if err != nil || !result.OK {
		t.Fatalf("slot not released: %+v %v", result, err)
	}
}
