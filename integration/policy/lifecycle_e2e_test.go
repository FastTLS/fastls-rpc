//go:build policy_e2e

package policy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/FastTLS/fastls-rpc/internal/testprocess"
)

func TestServiceCooperativeShutdownAcrossEntries(t *testing.T) {
	workspace := policyGraph(t)
	rpc, root := directories(t)

	for _, entry := range []struct{ kind, dir, pkg string }{{"fetch", policyRepository(root, "fastls-fetch"), "."}, {"jsonrpc", rpc, "./jsonrpc"}, {"grpc", rpc, "./grpc"}} {
		t.Run(entry.kind, func(t *testing.T) {
			binary := build(t, entry.dir, entry.pkg, entry.kind, workspace)
			for _, protocol := range []string{"h1", "h2", "h3"} {
				t.Run(protocol, func(t *testing.T) {
					for _, mode := range []string{"short", "cancel", "default-short"} {
						t.Run(mode, func(t *testing.T) {
							var hits, canceled atomic.Int32
							release := make(chan struct{}, 1)
							defer close(release)
							handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								hits.Add(1)
								if mode == "cancel" {
									w.WriteHeader(200)
									fmt.Fprint(w, "start")
									w.(http.Flusher).Flush()
								}
								select {
								case <-release:
									fmt.Fprint(w, r.Proto)
								case <-r.Context().Done():
									canceled.Add(1)
								}
							})
							peer := httptest.NewUnstartedServer(handler)
							if protocol == "h2" {
								peer.EnableHTTP2 = true
								peer.StartTLS()
							} else {
								peer.Start()
							}
							defer func() { peer.CloseClientConnections(); peer.Close() }()
							url := peer.URL
							params := map[string]any{"url": url, "timeout": 10}
							ca := ""
							if protocol == "h3" {
								h3url, rootFile, _, _ := h3Peer(t, handler)
								params["url"] = h3url
								params["http_version"] = "h3"
								ca = rootFile
							}
							audit := filepath.Join(t.TempDir(), "audit.jsonl")
							args := []string{"-h3-ca-file", ca, "-audit-file", audit}
							if mode != "default-short" {
								args = append(args, "-shutdown-cancel-after", "500ms")
							}
							call, process := h3Start(t, entry.kind, binary, nil, args...)
							resultCh := make(chan h3Reply, 1)
							errorCh := make(chan error, 1)
							ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
							defer cancel()
							go func() { r, err := call(ctx, params); resultCh <- r; errorCh <- err }()
							for deadline := time.Now().Add(3 * time.Second); ; {
								if hits.Load() > 0 {
									break
								}
								if time.Now().After(deadline) {
									t.Fatal("upstream never started", process.Logs())
								}
								time.Sleep(5 * time.Millisecond)
							}
							// All peers hold the response until after admission has stopped.
							start := time.Now()
							signal := os.Interrupt
							if runtime.GOOS != "windows" && protocol == "h2" {
								signal = syscall.SIGTERM
							}
							if err := process.Signal(signal); err != nil {
								t.Fatal("real stop signal:", err)
							}
							if mode != "cancel" {
								time.AfterFunc(100*time.Millisecond, func() { release <- struct{}{} })
							}
							var reply h3Reply
							select {
							case reply = <-resultCh:
							case <-ctx.Done():
								t.Fatal("caller not released")
							}
							err := <-errorCh
							if mode != "cancel" {
								want := map[string]string{"h1": "HTTP/1.1", "h2": "HTTP/2.0", "h3": "HTTP/3.0"}[protocol]
								if err != nil || !reply.OK || reply.Protocol != want {
									t.Fatalf("short response: %+v %v", reply, err)
								}
							} else {
								if err == nil && (reply.OK || reply.ErrorCode != "cancelled") {
									t.Fatalf("cancel response: %+v", reply)
								}
								for deadline := time.Now().Add(time.Second); ; {
									if canceled.Load() > 0 {
										break
									}
									if time.Now().After(deadline) {
										t.Fatal("upstream survived cooperative cancellation")
									}
									time.Sleep(5 * time.Millisecond)
								}
							}
							if err := process.Wait(6 * time.Second); err != nil {
								t.Fatal("cooperative process exit:", err, process.Logs())
							}
							if time.Since(start) > 5*time.Second {
								t.Fatal("shared total deadline exceeded")
							}
							logs := process.Logs()
							if !strings.Contains(logs, `"drain_expired":false`) {
								t.Fatal("audit did not drain", logs)
							}
							events, err := os.ReadFile(audit)
							if err != nil || len(events) == 0 {
								t.Fatal("missing audit completions", err)
							}
							if mode != "cancel" && strings.Contains(string(events), `"error_code":"cancelled"`) {
								t.Fatal("successful request was audited as canceled", string(events))
							}
							if mode == "cancel" && !strings.Contains(string(events), `"error_code":"cancelled"`) {
								t.Fatal("cancellation missing from audit", string(events))
							}
							t.Logf("lifecycle entry=%s protocol=%s mode=%s exit=0 seconds=%.3f upstream_reclaimed=true audit_drained=true", entry.kind, protocol, mode, time.Since(start).Seconds())
						})
					}
				})
			}
			// An unfinished transport handshake has no cooperative application work.
			// Both modes must retain the bounded, nonzero forced fallback.
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("unfinished-headers/cooperative=%v", enabled), func(t *testing.T) {
					address := testprocess.ReserveLoopbackAddress(t)
					args := []string{"-addr", address}
					if enabled {
						args = append(args, "-shutdown-cancel-after", "200ms")
					}
					process := testprocess.Start(t, binary, args...)
					var connection net.Conn
					var err error
					for deadline := time.Now().Add(3 * time.Second); ; {
						connection, err = net.DialTimeout("tcp", address, 100*time.Millisecond)
						if err == nil {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal(err, process.Logs())
						}
						time.Sleep(10 * time.Millisecond)
					}
					defer connection.Close()
					if entry.kind != "grpc" {
						fmt.Fprint(connection, "POST / HTTP/1.1\r\nHost: localhost\r\nX-Hold: ")
					}
					time.Sleep(100 * time.Millisecond)
					if err := process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
					err = process.Wait(7 * time.Second)
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() == 0 {
						t.Fatal("forced fallback must fail", err, process.Logs())
					}
					t.Logf("forced entry=%s cooperative=%v exit=%d", entry.kind, enabled, exit.ExitCode())
				})
			}
			for _, value := range []string{"-1s", "5s", "6s"} {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				output, err := exec.CommandContext(ctx, binary, "-addr", "127.0.0.1:0", "-shutdown-cancel-after", value).CombinedOutput()
				cancel()
				if err == nil || !strings.Contains(string(output), "shutdown-cancel-after must be") {
					t.Fatal("invalid startup interval", value, err, string(output))
				}
			}
		})
	}
	if output := os.Getenv("RELEASE_EVIDENCE"); output != "" {
		data, _ := json.MarshalIndent(map[string]any{"status": "passed", "platform": runtime.GOOS + "-" + runtime.GOARCH, "entries": 3, "protocols": []string{"h1", "h2", "h3"}, "real_console_break": runtime.GOOS == "windows", "default_drain_preserved": true, "forced_exit_nonzero": true}, "", "  ")
		if err := os.WriteFile(filepath.Join(output, "service-lifecycle.json"), append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
