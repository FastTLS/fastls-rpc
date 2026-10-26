//go:build policy_e2e

package policy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fastls "github.com/FastTLS/fastls"
	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type result struct {
	OK        bool   `json:"ok"`
	Body      string `json:"body"`
	Error     string `json:"error"`
	ErrorCode string `json:"error_code"`
	Protocol  string `json:"protocol"`
}

func directories(t *testing.T) (string, string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find integration source")
	}
	rpc := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	return rpc, filepath.Dir(rpc)
}

func build(t *testing.T, directory, pkg, name, workspace string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", output, pkg)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK="+workspace)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, data)
	}
	return output
}

func policyFile(t *testing.T, config any) string {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "policy.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func failedStartup(t *testing.T, binary, path, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, binary, "-addr", "127.0.0.1:0", "-address-policy", path).CombinedOutput()
	if ctx.Err() != nil || err == nil || !strings.Contains(string(data), want) {
		t.Fatalf("startup must fail before listening: %v %s", err, data)
	}
}

func start(t *testing.T, kind, binary string, config any) (func(map[string]any) result, *testprocess.Process) {
	t.Helper()
	address := testprocess.ReserveLoopbackAddress(t)
	path := policyFile(t, config)
	arguments := []string{"-addr", address, "-max-inflight", "1"}
	if config != nil {
		arguments = append(arguments, "-address-policy", path)
	}
	process := testprocess.Start(t, binary, arguments...)
	var client pb.FastlsServiceClient
	if kind == "grpc" {
		connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection.Close() })
		client = pb.NewFastlsServiceClient(connection)
		for deadline := time.Now().Add(5 * time.Second); ; {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			_, err = client.Health(ctx, &pb.HealthRequest{})
			cancel()
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("health: %v\n%s", err, process.Logs())
			}
			time.Sleep(20 * time.Millisecond)
		}
	} else {
		testprocess.WaitForHTTPStatus(t, "http://"+address+"/health", 200, process)
	}
	// Changes to the file after startup must not relax the compiled policy.
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	return func(params map[string]any) result {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if kind == "grpc" {
			// Request JSON uses the same snake_case fields as protobuf JSON
			// except legacy method-specific names; this subset is identical.
			request := &pb.FetchRequest{Url: params["url"].(string)}
			if value, ok := params["proxy"].(string); ok {
				request.Proxy = value
			}
			if value, ok := params["parent_proxy"].(string); ok {
				request.ParentProxy = value
			}
			if value, ok := params["headers"].(map[string]string); ok {
				request.Headers = value
			}
			if value, ok := params["disableRedirect"].(bool); ok {
				request.DisableRedirect = value
			}
			if value, ok := params["fingerprint"].(map[string]string); ok {
				request.Fingerprint = &pb.Fingerprint{Type: value["type"], Value: value["value"]}
			}
			response, err := client.Fetch(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			return result{OK: response.Ok, Body: response.Body, Error: response.Error, ErrorCode: response.ErrorCode, Protocol: response.Protocol}
		}
		var envelope any = params
		endpoint := "/fetch"
		if kind == "jsonrpc" {
			endpoint = "/rpc"
			envelope = map[string]any{"jsonrpc": "2.0", "method": "fetch", "id": 1, "params": params}
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(ctx, "POST", "http://"+address+endpoint, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if headers, ok := params["inbound_headers"].(map[string]string); ok {
			for name, value := range headers {
				request.Header.Set(name, value)
			}
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("HTTP status %d", response.StatusCode)
		}
		if kind == "jsonrpc" {
			var reply struct {
				Result result `json:"result"`
				Error  any    `json:"error"`
			}
			if err = json.NewDecoder(response.Body).Decode(&reply); err != nil || reply.Error != nil {
				t.Fatalf("RPC error %v %v", reply.Error, err)
			}
			return reply.Result
		}
		var reply result
		if err = json.NewDecoder(response.Body).Decode(&reply); err != nil {
			t.Fatal(err)
		}
		return reply
	}, process
}

func TestServiceAddressPolicyAcrossEntries(t *testing.T) {
	workspace := os.Getenv("GOWORK")
	if workspace == "" || workspace == "off" || !filepath.IsAbs(workspace) {
		t.Fatal("policy_e2e requires an explicit absolute GOWORK; it must not silently use released dependencies")
	}
	if _, ok := any(fastls.NewClient()).(interface {
		WithAddressPolicyJSON([]byte) (fastls.Fastls, error)
	}); !ok {
		t.Fatal("policy_e2e requires the new core source")
	}
	rpc, root := directories(t)
	binaries := map[string]string{
		"fetch":   build(t, filepath.Join(root, "fastls-fetch-ftransport"), ".", "fetch", workspace),
		"jsonrpc": build(t, rpc, "./jsonrpc", "jsonrpc", workspace),
		"grpc":    build(t, rpc, "./grpc", "grpc", workspace),
	}
	var hits, forbidden, proxyHits atomic.Int32
	forbiddenPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forbidden.Add(1) }))
	defer forbiddenPeer.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyHits.Add(1) }))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, strings.Replace(forbiddenPeer.URL, "127.0.0.1", "localhost", 1), 302)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer target.Close()
	tlsTarget := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "tls-ok") }))
	tlsTarget.EnableHTTP2 = true
	tlsTarget.StartTLS()
	defer tlsTarget.Close()
	u, _ := url.Parse(target.URL)
	port := json.Number(u.Port())
	for _, kind := range []string{"fetch", "jsonrpc", "grpc"} {
		t.Run(kind, func(t *testing.T) {
			config := map[string]any{"allow_schemes": []string{"http"}, "allow_hosts": []string{"127.0.0.1"}, "allow_ports": []any{port}, "allow_cidrs": []string{"127.0.0.1/32"}}
			call, process := start(t, kind, binaries[kind], config)
			allowed := func() {
				t.Helper()
				response := call(map[string]any{"url": target.URL + "/ok"})
				if !response.OK || response.Body != "ok" {
					t.Fatalf("allowed request: %+v", response)
				}
			}
			allowed()
			proxyURL := strings.Replace(proxy.URL, "http://", "http://user:p03-policy-secret@", 1)
			for _, test := range []struct {
				name, reason string
				params       map[string]any
			}{
				{"host", "host_not_allowed", map[string]any{"url": strings.Replace(target.URL, "127.0.0.1", "localhost", 1)}},
				{"port", "port_not_allowed", map[string]any{"url": forbiddenPeer.URL}},
				{"scheme", "scheme_not_allowed", map[string]any{"url": strings.Replace(target.URL, "http:", "https:", 1)}},
				{"host override", "host_override", map[string]any{"url": target.URL, "headers": map[string]string{"Host": "forbidden.test"}}},
				{"proxy", "proxy_unsupported", map[string]any{"url": target.URL, "proxy": proxyURL}},
				{"parent proxy", "proxy_unsupported", map[string]any{"url": target.URL, "parent_proxy": proxyURL}},
				{"HTTP3", "http3_unsupported", map[string]any{"url": target.URL, "fingerprint": map[string]string{"type": "ja4r", "value": "q13d1517h2_002f,0035,009c,009d,1301,1302,1303,c013,c014,c02b,c02c,c02f,c030,cca8,cca9_0005,000a,000b,000d,0012,0017,001b,0023,0029,002b,002d,0033,44cd,fe0d,ff01_0403,0804,0401,0503,0805,0501,0806,0601"}}},
				{"userinfo", "invalid_url", map[string]any{"url": strings.Replace(target.URL, "http://", "http://user:p03-policy-secret@", 1) + "/?token=p03-policy-secret"}},
				{"DTO cannot relax policy", "host_not_allowed", map[string]any{"url": strings.Replace(target.URL, "127.0.0.1", "localhost", 1), "address_policy": map[string]any{}, "address_policy_file": "", "AddressPolicy": nil}},
			} {
				t.Run(test.name, func(t *testing.T) {
					before := hits.Load()
					response := call(test.params)
					if response.OK || response.ErrorCode != "address_denied" || !strings.Contains(response.Error, test.reason) || strings.Contains(response.Error, "p03-policy-secret") {
						t.Fatalf("denial mismatch: %+v", response)
					}
					if hits.Load() != before || forbidden.Load() != 0 || proxyHits.Load() != 0 {
						t.Fatal("denied request reached network peer")
					}
					allowed() // Each denial must release the one available request slot.
				})
			}
			if kind == "fetch" {
				response := call(map[string]any{"url": target.URL, "inbound_headers": map[string]string{"X-Mitm-Proxy": proxyURL}})
				if response.OK || response.ErrorCode != "address_denied" || !strings.Contains(response.Error, "proxy_unsupported") {
					t.Fatalf("inbound proxy header bypass: %+v", response)
				}
				allowed()
			}
			response := call(map[string]any{"url": target.URL + "/redirect?token=p03-policy-secret"})
			if response.OK || response.ErrorCode != "address_denied" || !strings.Contains(response.Error, "host_not_allowed") {
				t.Fatalf("redirect: %+v", response)
			}
			response = call(map[string]any{"url": target.URL + "/redirect", "disableRedirect": true})
			if !response.OK {
				t.Fatalf("disabled redirect: %+v", response)
			}
			if forbidden.Load() != 0 || proxyHits.Load() != 0 {
				t.Fatal("redirect/proxy reached forbidden peer")
			}
			if logs := process.Logs(); strings.Contains(logs, "p03-policy-secret") {
				t.Fatal("policy-denial log leaked URL secret")
			}

			for _, host := range []string{"127.0.0.1", "localhost"} {
				t.Run("CIDR "+host, func(t *testing.T) {
					call, _ := start(t, kind, binaries[kind], map[string]any{"allow_cidrs": []string{"0.0.0.0/0", "::/0"}, "deny_cidrs": []string{"127.0.0.0/8", "::1/128"}})
					before := hits.Load()
					response := call(map[string]any{"url": strings.Replace(target.URL, "127.0.0.1", host, 1)})
					if response.OK || response.ErrorCode != "address_denied" || !strings.Contains(response.Error, "cidr_denied") || hits.Load() != before {
						t.Fatalf("CIDR denial: %+v", response)
					}
				})
			}
			t.Run("HTTP2 remains usable", func(t *testing.T) {
				call, _ := start(t, kind, binaries[kind], map[string]any{"allow_schemes": []string{"https"}, "allow_hosts": []string{"127.0.0.1"}, "allow_cidrs": []string{"127.0.0.1/32"}})
				response := call(map[string]any{"url": tlsTarget.URL})
				if !response.OK || response.Body != "tls-ok" || response.Protocol != "HTTP/2.0" {
					t.Fatalf("H2 policy response: %+v", response)
				}
			})
			for _, invalid := range []any{nil, map[string]any{"allow_hosts": nil}, map[string]any{"unknown": true}} {
				failedStartup(t, binaries[kind], policyFile(t, invalid), "invalid address policy")
			}
		})
	}
}

func TestReleasedCoreRefusesRequestedPolicy(t *testing.T) {
	rpc, root := directories(t)
	for _, entry := range []struct{ kind, dir, pkg string }{{"fetch", filepath.Join(root, "fastls-fetch-ftransport"), "."}, {"jsonrpc", rpc, "./jsonrpc"}, {"grpc", rpc, "./grpc"}} {
		t.Run(entry.kind, func(t *testing.T) {
			// Explicitly compile with the still-pinned v0.1.1 baseline. Keep this
			// test synchronized when the public dependency is intentionally upgraded.
			binary := build(t, entry.dir, entry.pkg, entry.kind, "off")
			failedStartup(t, binary, policyFile(t, map[string]any{}), "policy-capable fastls core")
		})
	}
}
