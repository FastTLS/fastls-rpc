//go:build workspace_e2e

package workspace_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	fastls "github.com/FastTLS/fastls"
	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	fastlsimitate "github.com/FastTLS/fastls/imitate"
	"github.com/FastTLS/imitate"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// All entries use the same origin, bytes and failure injection. Build every
// service with the same explicit dependency mode; never silently replace it.
func TestWorkspaceUnifiedResponseContract(t *testing.T) {
	rpcRoot, root := workspaceDirectories(t)
	workspace := os.Getenv("FASTLS_CONTRACT_GOWORK")
	if workspace == "" {
		workspace = "off"
	}
	build := func(dir, pkg, name string) string {
		t.Helper()
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		out := filepath.Join(t.TempDir(), name)
		cmd := exec.Command("go", "build", "-trimpath", "-o", out, pkg)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK="+workspace)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, output)
		}
		return out
	}
	fetchBinary := build(filepath.Join(root, "fastls-fetch-ftransport"), ".", "fetch")
	rpcBinary := build(rpcRoot, "./jsonrpc", "jsonrpc")
	grpcBinary := build(rpcRoot, "./grpc", "grpc")
	mitmBinary := build(filepath.Join(root, "fastls-mitm-ftransport"), ".", "mitm")
	addresses := map[string]string{}
	var mitmCA string
	for kind, binary := range map[string]string{"fetch": fetchBinary, "jsonrpc": rpcBinary, "grpc": grpcBinary, "mitm": mitmBinary} {
		address := testprocess.ReserveLoopbackAddress(t)
		addresses[kind] = address
		args := []string{"-addr", address}
		if os.Getenv("FASTLS_CONTRACT_AUDIT") == "1" {
			args = append(args, "-audit-file", filepath.Join(t.TempDir(), kind+".jsonl"))
		}
		if kind == "mitm" {
			dir := t.TempDir()
			mitmCA = filepath.Join(dir, "ca.pem")
			args = append(args, "-ca-cert", mitmCA, "-ca-key", filepath.Join(dir, "key.pem"))
		}
		p := testprocess.Start(t, binary, args...)
		if kind == "grpc" {
			assertGRPCHealth(t, address, p)
		} else {
			path := "/health"
			if kind == "mitm" {
				path = "/"
			}
			testprocess.WaitForHTTPStatus(t, "http://"+address+path, 200, p)
		}
	}
	grpcConn, grpcClient := newGRPCClient(t, addresses["grpc"])
	defer grpcConn.Close()
	payload := []byte{0, 255, 128, 1}
	var encoded bytes.Buffer
	zw := gzip.NewWriter(&encoded)
	zw.Write(payload)
	zw.Close()
	for _, protocol := range []string{"http", "https-h1", "https-h2"} {
		t.Run(protocol, func(t *testing.T) {
			cancelStarted, cancelEnded := make(chan struct{}, 1), make(chan struct{}, 1)
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Origin-Protocol", r.Proto)
				switch r.URL.Path {
				case "/cancel":
					cancelStarted <- struct{}{}
					<-r.Context().Done()
					cancelEnded <- struct{}{}
				case "/redirect":
					http.Redirect(w, r, "/binary", 302)
				case "/gzip":
					w.Header().Set("Content-Encoding", "gzip")
					w.Write(encoded.Bytes())
				case "/broken-gzip":
					w.Header().Set("Content-Encoding", "gzip")
					w.Write([]byte("bad"))
				case "/truncated":
					w.Header().Set("Content-Length", "100")
					w.Write([]byte("short"))
				case "/reset":
					w.Write([]byte("prefix"))
					w.(http.Flusher).Flush()
					panic(http.ErrAbortHandler)
				case "/headers-hold":
					<-r.Context().Done()
				case "/body-hold":
					w.Write([]byte("prefix"))
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				default:
					w.Header().Add("X-Repeat", "one")
					w.Header().Add("X-Repeat", "two")
					w.Header().Add("Set-Cookie", "a=1; Path=/")
					w.Header().Add("Set-Cookie", "b=2; Path=/")
					w.Header().Set("Trailer", "X-Final")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					w.Write(payload)
					w.Header().Set("X-Final", "complete")
				}
			}))
			if protocol == "http" {
				target.Start()
			} else {
				target.EnableHTTP2 = protocol == "https-h2"
				target.StartTLS()
			}
			defer target.Close()
			defer target.CloseClientConnections()
			var originCA string
			if protocol != "http" {
				originCA = filepath.Join(t.TempDir(), "origin-ca.pem")
				if err := os.WriteFile(originCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: target.Certificate().Raw}), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, route := range []string{"direct", "mitm"} {
				t.Run(route, func(t *testing.T) {
					proxy := ""
					if route == "mitm" {
						proxy = "http://" + addresses["mitm"]
					}
					for _, entry := range []string{"net-http", "core", "fetch", "jsonrpc", "grpc"} {
						t.Run(entry, func(t *testing.T) {
							call := func(ctx context.Context, path string, follow bool) (contractResponse, error) {
								endpoint := target.URL + path
								if entry == "core" {
									opts := fastls.Options{Proxy: proxy, Timeout: 1, DisableRedirect: !follow, Headers: map[string]string{"Accept-Encoding": "gzip"}}
									if _, err := fastlsimitate.Use(&opts, "firefox/158/windows", fastlsimitate.WithProvider(imitate.New())); err != nil {
										return contractResponse{}, err
									}
									resp, err := fastls.NewClient().DoContext(ctx, endpoint, opts, "GET")
									defer resp.Client.CloseIdleConnections()
									if err != nil {
										return contractResponse{}, err
									}
									defer resp.Body.Close()
									body, err := io.ReadAll(resp.Body)
									return contractResponse{OK: err == nil, Status: resp.Status, Protocol: resp.Protocol, FinalURL: resp.FinalURL, HeaderValues: http.Header(resp.HeaderValues), Trailers: resp.Trailer, BodyBase64: base64.StdEncoding.EncodeToString(body)}, err
								}
								if entry == "net-http" {
									transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true, DisableCompression: true}
									if proxy != "" {
										u, _ := url.Parse(proxy)
										transport.Proxy = http.ProxyURL(u)
									}
									defer transport.CloseIdleConnections()
									client := &http.Client{Transport: transport, Timeout: time.Second}
									if !follow {
										client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
									}
									req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
									req.Header.Set("Accept-Encoding", "gzip")
									resp, err := client.Do(req)
									if err != nil {
										return contractResponse{}, err
									}
									defer resp.Body.Close()
									body, err := io.ReadAll(resp.Body)
									return contractResponse{OK: err == nil, Status: resp.StatusCode, Protocol: resp.Proto, FinalURL: resp.Request.URL.String(), HeaderValues: resp.Header, Trailers: resp.Trailer, BodyBase64: base64.StdEncoding.EncodeToString(body)}, err
								}
								if entry == "grpc" {
									resp, err := grpcClient.Fetch(ctx, &pb.FetchRequest{Url: endpoint, ParentProxy: proxy, Timeout: 1, DisableRedirect: !follow, IncludeBodyBase64: true, Headers: map[string]string{"Accept-Encoding": "gzip"}})
									if err != nil {
										return contractResponse{}, err
									}
									headers, trailers := http.Header{}, http.Header{}
									for n, v := range resp.HeaderValues {
										headers[n] = v.Values
									}
									for n, v := range resp.Trailers {
										trailers[n] = v.Values
									}
									return contractResponse{OK: resp.Ok, Status: int(resp.Status), Protocol: resp.Protocol, FinalURL: resp.FinalUrl, HeaderValues: headers, Trailers: trailers, BodyBase64: resp.BodyBase64, Body: resp.Body, ErrorCode: resp.ErrorCode, TimeoutPhase: resp.TimeoutPhase}, nil
								}
								params := map[string]any{"url": endpoint, "parent_proxy": proxy, "timeout": 1, "disableRedirect": !follow, "include_body_base64": true, "headers": map[string]string{"Accept-Encoding": "gzip"}}
								var data any = params
								api := "/fetch"
								if entry == "jsonrpc" {
									data = map[string]any{"jsonrpc": "2.0", "method": "fetch", "id": 1, "params": params}
									api = "/rpc"
								}
								body, _ := json.Marshal(data)
								req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addresses[entry]+api, bytes.NewReader(body))
								req.Header.Set("Content-Type", "application/json")
								resp, err := http.DefaultClient.Do(req)
								if err != nil {
									return contractResponse{}, err
								}
								defer resp.Body.Close()
								var result contractResponse
								if entry == "jsonrpc" {
									var envelope struct {
										Result contractResponse
										Error  any
									}
									err = json.NewDecoder(resp.Body).Decode(&envelope)
									if envelope.Error != nil {
										t.Fatalf("RPC envelope: %v", envelope.Error)
									}
									result = envelope.Result
								} else {
									err = json.NewDecoder(resp.Body).Decode(&result)
								}
								return result, err
							}
							if protocol == "http" && route == "mitm" && entry != "net-http" {
								ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
								defer cancel()
								result, err := call(ctx, "/binary", false)
								if entry == "core" {
									if err == nil {
										t.Fatal("cleartext CONNECT unexpectedly succeeded; review compatibility boundary")
									}
								} else if err != nil || result.OK || result.ErrorCode != "request_error" {
									t.Fatalf("cleartext CONNECT result=%+v err=%v", result, err)
								}
								t.Log("Known compatibility difference: core uses CONNECT for an HTTP destination; default MITM expects TLS after CONNECT. Absolute-form HTTP proxy requests are covered by net-http and the MITM binary suite.")
								return
							}
							t.Run("caller-cancel-releases-origin", func(t *testing.T) {
								ctx, cancel := context.WithCancel(context.Background())
								defer cancel()
								done := make(chan error, 1)
								go func() { _, err := call(ctx, "/cancel", false); done <- err }()
								select {
								case <-cancelStarted:
								case <-time.After(2 * time.Second):
									t.Fatal("cancel origin never started")
								}
								cancel()
								select {
								case err := <-done:
									if !errors.Is(err, context.Canceled) && status.Code(err) != codes.Canceled {
										t.Fatalf("cancel result=%v", err)
									}
								case <-time.After(2 * time.Second):
									t.Fatal("caller cancellation blocked")
								}
								select {
								case <-cancelEnded:
								case <-time.After(2 * time.Second):
									t.Fatal("caller cancellation retained origin")
								}
							})
							for _, tc := range []struct {
								path    string
								follow  bool
								failure string
							}{{"/binary", false, ""}, {"/gzip", false, ""}, {"/redirect", false, ""}, {"/redirect", true, ""}, {"/truncated", false, "read_error"}, {"/reset", false, "read_error"}, {"/broken-gzip", false, "decode_error"}, {"/headers-hold", false, "timeout"}, {"/body-hold", false, "timeout"}} {
								t.Run(tc.path+map[bool]string{true: "-follow"}[tc.follow], func(t *testing.T) {
									ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
									defer cancel()
									result, err := call(ctx, tc.path, tc.follow)
									failure := tc.failure
									if (entry == "net-http" || entry == "core") && failure == "decode_error" {
										failure = ""
									}
									if failure != "" {
										if entry == "core" || entry == "net-http" {
											if err == nil {
												t.Fatal("upstream failure became success")
											}
										} else if err != nil || result.OK || result.ErrorCode != failure {
											t.Fatalf("failure=%+v err=%v", result, err)
										}
										if failure == "timeout" && entry != "core" && entry != "net-http" && result.TimeoutPhase != "total" {
											t.Fatalf("timeout phase=%q", result.TimeoutPhase)
										}
										return
									}
									if err != nil || !result.OK {
										t.Fatalf("result=%+v err=%v", result, err)
									}
									wantURL := target.URL + tc.path
									status := 200
									if tc.path == "/redirect" {
										if tc.follow {
											wantURL = target.URL + "/binary"
										} else {
											status = 302
										}
									}
									if result.Status != status || result.FinalURL != wantURL {
										t.Fatalf("status/final URL: %+v", result)
									}
									wantProto := "HTTP/1.1"
									if route == "direct" && protocol == "https-h2" {
										wantProto = "HTTP/2.0"
									}
									if result.Protocol != wantProto {
										t.Fatalf("protocol=%s want=%s", result.Protocol, wantProto)
									}
									body, err := base64.StdEncoding.DecodeString(result.BodyBase64)
									if err != nil {
										t.Fatal(err)
									}
									if tc.path == "/binary" || tc.follow {
										if !bytes.Equal(body, payload) || !reflect.DeepEqual(result.HeaderValues.Values("X-Repeat"), []string{"one", "two"}) || len(result.HeaderValues.Values("Set-Cookie")) != 2 || result.Trailers.Get("X-Final") != "complete" {
											t.Fatalf("binary/header/trailer: %+v", result)
										}
									}
									if tc.path == "/gzip" && !bytes.Equal(body, encoded.Bytes()) {
										t.Fatalf("gzip wire bytes=%x", body)
									}
									if tc.path == "/gzip" {
										if result.HeaderValues.Get("Content-Encoding") != "gzip" {
											t.Fatal("wire encoding header lost")
										}
										if entry != "core" && entry != "net-http" && result.Body != string(bytes.ToValidUTF8(payload, []byte("\uFFFD"))) {
											t.Fatalf("legacy decoded display=%q", result.Body)
										}
									}
								})
							}
						})
					}
					if python, script := os.Getenv("FASTLS_CONTRACT_PYTHON"), os.Getenv("FASTLS_CONTRACT_FFI_SCRIPT"); python != "" && script != "" {
						t.Run("python-ffi", func(t *testing.T) {
							ca := originCA
							if proxy != "" && protocol != "http" {
								ca = mitmCA
							}
							cmd := exec.Command(python, script, "--target", target.URL, "--proxy", proxy, "--ca", ca)
							if out, err := cmd.CombinedOutput(); err != nil {
								t.Fatalf("FFI: %v\n%s", err, out)
							} else {
								t.Log(string(out))
							}
						})
					}
				})
			}
		})
	}
}
