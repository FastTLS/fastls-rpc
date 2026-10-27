//go:build workspace_e2e

package workspace_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type auditEvent struct {
	Schema         int    `json:"schema_version"`
	ID             string `json:"request_id"`
	Parent         string `json:"parent_id"`
	Entry          string `json:"entry"`
	Kind           string `json:"kind"`
	Downstream     string `json:"downstream_protocol"`
	Upstream       string `json:"upstream_protocol"`
	Code           string `json:"error_code"`
	Phase          string `json:"timeout_phase"`
	RPCCode        string `json:"rpc_code"`
	UpstreamStatus int    `json:"upstream_status"`
}

func TestWorkspaceAuditBinaries(t *testing.T) {
	rpcRoot, root := workspaceDirectories(t)
	work := os.Getenv("FASTLS_CONTRACT_GOWORK")
	if work == "" {
		work = "off"
	}
	for _, kind := range []string{"fetch", "jsonrpc", "grpc", "mitm"} {
		t.Run(kind, func(t *testing.T) {
			dir, pkg := rpcRoot, "./"+kind
			if kind == "fetch" || kind == "mitm" {
				dir = filepath.Join(root, "fastls-"+kind+"-ftransport")
				pkg = "."
			}
			name := kind
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			binary := filepath.Join(t.TempDir(), name)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, pkg)
			build.Dir = dir
			build.Env = append(os.Environ(), "GOWORK="+work)
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, out)
			}
			bad := exec.Command(binary, "-audit-queue", "-1")
			if out, err := bad.CombinedOutput(); err == nil || !bytes.Contains(out, []byte("audit-queue")) {
				t.Fatalf("invalid audit config: %v %s", err, out)
			}
			bad = exec.Command(binary, "-audit-file", filepath.Join(t.TempDir(), "missing", "audit.jsonl"))
			if out, err := bad.CombinedOutput(); err == nil {
				t.Fatalf("invalid audit file accepted: %s", out)
			}
			started, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/hold":
					started <- struct{}{}
					<-r.Context().Done()
					cancelled <- struct{}{}
					panic(http.ErrAbortHandler)
				case "/broken":
					w.Header().Set("Content-Encoding", "gzip")
					w.Write([]byte("bad"))
				case "/truncated":
					w.Header().Set("Content-Length", "100")
					w.Write([]byte("short"))
				default:
					w.Header().Set("Trailer", "X-Final")
					w.Header().Add("X-Multi", "one")
					w.Header().Add("X-Multi", "two")
					w.Write([]byte{0, 255, 128, 1})
					w.Header().Set("X-Final", "done")
				}
			}))
			target.EnableHTTP2 = true
			target.StartTLS()
			defer target.Close()
			defer target.CloseClientConnections()
			address := testprocess.ReserveLoopbackAddress(t)
			folder := t.TempDir()
			auditFile := filepath.Join(folder, "audit.jsonl")
			ca := filepath.Join(folder, "ca.pem")
			args := []string{"-addr", address, "-audit-file", auditFile, "-audit-queue", "32"}
			if kind == "mitm" {
				args = append(args, "-ca-cert", ca, "-ca-key", filepath.Join(folder, "key.pem"))
			} else {
				args = append(args, "-max-admitted-requests", "1", "-max-request-bytes", "1024")
			}
			process := testprocess.Start(t, binary, args...)
			var call func(context.Context, string, string) (contractResponse, error)
			var closeClient func()
			if kind == "grpc" {
				assertGRPCHealth(t, address, process)
				connection, client := newGRPCClient(t, address)
				closeClient = func() { connection.Close() }
				if err := connection.Invoke(context.Background(), "/LEAK_SERVICE/LEAK_METHOD", &pb.HealthRequest{}, &pb.HealthResponse{}); status.Code(err) != codes.Unimplemented {
					t.Fatalf("unknown method=%v", err)
				}
				call = func(ctx context.Context, path, body string) (contractResponse, error) {
					r, err := client.Fetch(ctx, &pb.FetchRequest{Url: target.URL + path, Body: body, Timeout: 1, IncludeBodyBase64: true, Headers: map[string]string{"Authorization": "LEAK_AUTH", "Cookie": "LEAK_COOKIE"}})
					if err != nil {
						return contractResponse{}, err
					}
					return contractResponse{OK: r.Ok, ErrorCode: r.ErrorCode, Protocol: r.Protocol, BodyBase64: r.BodyBase64}, nil
				}
			} else {
				path := "/health"
				if kind == "mitm" {
					path = "/"
				}
				testprocess.WaitForHTTPStatus(t, "http://"+address+path, 200, process)
				transport := &http.Transport{}
				client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
				closeClient = transport.CloseIdleConnections
				if kind == "mitm" {
					pem, err := os.ReadFile(ca)
					if err != nil {
						t.Fatal(err)
					}
					roots := x509.NewCertPool()
					roots.AppendCertsFromPEM(pem)
					proxy, _ := url.Parse("http://" + address)
					transport.Proxy = http.ProxyURL(proxy)
					transport.TLSClientConfig = &tls.Config{RootCAs: roots}
				}
				call = func(ctx context.Context, path, body string) (contractResponse, error) {
					endpoint := "http://" + address + "/fetch"
					var data any = map[string]any{"url": target.URL + path, "timeout": 1, "body": body, "include_body_base64": true, "headers": map[string]string{"Authorization": "LEAK_AUTH", "Cookie": "LEAK_COOKIE"}}
					if kind == "jsonrpc" {
						endpoint = "http://" + address + "/rpc"
						data = map[string]any{"jsonrpc": "2.0", "id": "LEAK_USER_ID", "method": "fetch", "params": data}
					}
					payload, _ := json.Marshal(data)
					if kind == "mitm" {
						endpoint = target.URL + path
						payload = []byte(body)
					}
					req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payload))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "LEAK_AUTH")
					req.Header.Set("Cookie", "LEAK_COOKIE")
					req.Header.Set("X-Request-ID", "LEAK_ID")
					resp, err := client.Do(req)
					if err != nil {
						return contractResponse{}, err
					}
					defer resp.Body.Close()
					var result contractResponse
					if kind == "mitm" {
						body, err := io.ReadAll(resp.Body)
						if path == "/binary?token=LEAK_QUERY" && (!bytes.Equal(body, []byte{0, 255, 128, 1}) || len(resp.Header.Values("X-Multi")) != 2 || resp.Trailer.Get("X-Final") != "done") {
							t.Fatalf("audit changed forwarding: %x %v %v", body, resp.Header, resp.Trailer)
						}
						return contractResponse{OK: err == nil, Status: resp.StatusCode}, err
					}
					if kind == "jsonrpc" {
						var envelope struct {
							Result contractResponse
							Error  any
						}
						err = json.NewDecoder(resp.Body).Decode(&envelope)
						result = envelope.Result
					} else {
						err = json.NewDecoder(resp.Body).Decode(&result)
					}
					return result, err
				}
			}
			t.Cleanup(closeClient)
			readEvents := func() ([]auditEvent, []byte) {
				data, err := os.ReadFile(auditFile)
				if err != nil {
					t.Fatal(err)
				}
				var events []auditEvent
				for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
					if len(line) == 0 {
						continue
					}
					var e auditEvent
					if json.Unmarshal(line, &e) == nil {
						events = append(events, e)
					}
				}
				return events, data
			}
			waitCode := func(code string) auditEvent {
				t.Helper()
				for deadline := time.Now().Add(3 * time.Second); ; {
					events, _ := readEvents()
					for _, e := range events {
						if e.Code == code {
							return e
						}
					}
					if time.Now().After(deadline) {
						t.Fatalf("missing %q audit event: %+v", code, events)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if result, err := call(context.Background(), "/binary?token=LEAK_QUERY", "LEAK_BODY"); err != nil || !result.OK {
				t.Fatalf("normal result=%+v err=%v", result, err)
			}
			call(context.Background(), "/truncated", "")
			waitCode("read_error")
			if kind == "grpc" {
				e := waitCode("not_found")
				if e.RPCCode != "Unimplemented" {
					t.Fatalf("unknown RPC audit=%+v", e)
				}
			}
			if kind != "mitm" {
				call(context.Background(), "/broken", "")
				waitCode("decode_error")
				call(context.Background(), "/binary", strings.Repeat("x", 2048))
				waitCode("request_too_large")
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { call(ctx, "/hold", ""); close(done) }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				cancel()
				t.Fatal("hold not started")
			}
			if kind != "mitm" {
				call(context.Background(), "/binary", "")
				waitCode("overloaded")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("caller cancellation blocked")
			}
			select {
			case <-cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("origin not released")
			}
			waitCode("cancelled")
			if kind != "mitm" {
				call(context.Background(), "/hold", "")
				<-started
				<-cancelled
				e := waitCode("timeout")
				if e.Phase != "total" {
					t.Fatalf("phase=%s", e.Phase)
				}
			}
			closeClient()
			for deadline := time.Now().Add(2 * time.Second); ; {
				events, _ := readEvents()
				valid := false
				for _, e := range events {
					if e.UpstreamStatus == 200 && e.Upstream == "HTTP/2.0" && e.Code == "" {
						valid = true
						break
					}
				}
				if valid {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("missing real upstream protocol: %+v", events)
				}
				time.Sleep(10 * time.Millisecond)
			}
			graceful := stopAuditProcess(t, process)
			events, data := readEvents()
			if bytes.Contains(data, []byte("LEAK_")) {
				t.Fatalf("secret in audit: %s", data)
			}
			seen := map[string]bool{}
			for _, e := range events {
				if e.Schema != 1 || len(e.ID) != 32 || e.Entry != kind || seen[e.ID] {
					t.Fatalf("bad event=%+v", e)
				}
				seen[e.ID] = true
			}
			if graceful {
				logs := process.Logs()
				if !strings.Contains(logs, "audit_stats") || !strings.Contains(logs, "\"dropped_closed\":0") {
					t.Fatalf("missing drained audit stats: %s", logs)
				}
			}
			t.Logf("entry=%s events=%d graceful_cli=%v", kind, len(events), graceful)
		})
	}
}
