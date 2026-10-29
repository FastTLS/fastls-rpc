//go:build workspace_e2e

package workspace_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func ingressEventually(t *testing.T, probe func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if probe() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ingress resource did not recover")
}
func ingressDial(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	return conn
}
func ingressClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var data [1]byte
	n, err := conn.Read(data[:])
	if n != 0 || err == nil {
		t.Fatalf("rejected TCP connection returned data: %d %v", n, err)
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("excess TCP connection was queued instead of closed")
	}
}
func ingressHTTPHealthy(address string) bool {
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 250 * time.Millisecond}
	response, err := client.Get("http://" + address + "/health")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	return response.StatusCode == 200
}
func ingressStart(t *testing.T, kind, binary string, args ...string) (string, *testprocess.Process) {
	t.Helper()
	address := testprocess.ReserveLoopbackAddress(t)
	process := testprocess.Start(t, binary, append([]string{"-addr", address}, args...)...)
	if kind == "grpc" {
		assertGRPCHealth(t, address, process)
	} else {
		ingressEventually(t, func() bool { return ingressHTTPHealthy(address) })
	}
	return address, process
}

func TestWorkspaceIngressBinaries(t *testing.T) {
	rpc, root := workspaceDirectories(t)
	binaries := map[string]string{
		"fetch":   testprocess.BuildInDir(t, workspaceRepository(root, "fastls-fetch"), ".", "fetch"),
		"jsonrpc": testprocess.BuildInDir(t, rpc, "./jsonrpc", "jsonrpc"),
		"grpc":    testprocess.BuildInDir(t, rpc, "./grpc", "grpc"),
	}
	for _, kind := range []string{"fetch", "jsonrpc", "grpc"} {
		t.Run(kind, func(t *testing.T) {
			binary := binaries[kind]
			invalid := [][2]string{{"max-connections", "-1"}, {"max-connections", "65537"}, {"max-admitted-requests", "-1"}, {"max-admitted-requests", "65537"}, {"max-header-bytes", "-1"}, {"max-header-bytes", "16777217"}}
			if kind == "grpc" {
				invalid = append(invalid, [2]string{"max-streams-per-connection", "-1"}, [2]string{"max-streams-per-connection", "65537"}, [2]string{"connection-timeout", "-1s"}, [2]string{"rpc-timeout", "25h"})
			} else {
				invalid = append(invalid, [2]string{"read-timeout", "-1s"}, [2]string{"read-header-timeout", "25h"}, [2]string{"idle-timeout", "-1s"})
			}
			for _, option := range invalid {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				data, err := exec.CommandContext(ctx, binary, "-addr", "127.0.0.1:0", "-"+option[0], option[1]).CombinedOutput()
				cancel()
				if err == nil || ctx.Err() == context.DeadlineExceeded || !strings.Contains(string(data), option[0]+" must be") {
					t.Fatalf("invalid option accepted or validated after startup: %v %s", err, data)
				}
			}
			t.Run("TCP permits include idle connections", func(t *testing.T) {
				address, _ := ingressStart(t, kind, binary, "-max-connections", "1")
				var release func()
				if kind == "grpc" {
					connection, client := newGRPCClient(t, address)
					defer connection.Close()
					ingressEventually(t, func() bool {
						ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
						defer cancel()
						_, err := client.Health(ctx, &pb.HealthRequest{})
						return err == nil
					})
					release = func() { connection.Close() }
				} else {
					var conn net.Conn
					// The readiness probe's closing TCP lease may still be retiring.
					ingressEventually(t, func() bool {
						candidate, err := net.DialTimeout("tcp", address, time.Second)
						if err != nil {
							return false
						}
						candidate.SetDeadline(time.Now().Add(time.Second))
						fmt.Fprint(candidate, "GET /health HTTP/1.1\r\nHost: local\r\n\r\n")
						response, err := http.ReadResponse(bufio.NewReader(candidate), nil)
						if err != nil {
							candidate.Close()
							return false
						}
						io.Copy(io.Discard, response.Body)
						response.Body.Close()
						if response.StatusCode != 200 {
							candidate.Close()
							return false
						}
						conn = candidate
						return true
					})
					t.Cleanup(func() { conn.Close() })
					// The same accepted connection now stalls before completing its next headers.
					fmt.Fprint(conn, "GET /health HTTP/1.1\r\nHo")
					release = func() { conn.Close() }
				}
				excess := ingressDial(t, address)
				ingressClosed(t, excess)
				release()
				if kind == "grpc" {
					connection, client := newGRPCClient(t, address)
					defer connection.Close()
					ingressEventually(t, func() bool {
						ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
						defer cancel()
						_, err := client.Health(ctx, &pb.HealthRequest{})
						return err == nil
					})
				} else {
					ingressEventually(t, func() bool { return ingressHTTPHealthy(address) })
				}
			})
			if kind != "grpc" {
				path := "/fetch"
				if kind == "jsonrpc" {
					path = "/rpc"
				}
				t.Run("admission before body and read deadline", func(t *testing.T) {
					address, _ := ingressStart(t, kind, binary, "-max-admitted-requests", "1", "-read-timeout", "350ms")
					holder := ingressDial(t, address)
					reader := bufio.NewReader(holder)
					fmt.Fprintf(holder, "POST %s HTTP/1.1\r\nHost: local\r\nContent-Type: application/json\r\nContent-Length: 1000\r\nExpect: 100-continue\r\n\r\n", path)
					response, err := http.ReadResponse(reader, nil)
					if err != nil || response.StatusCode != 100 {
						t.Fatalf("body reader did not begin: %v %v", response, err)
					}
					response.Body.Close()
					rejected := ingressDial(t, address)
					fmt.Fprintf(rejected, "POST %s HTTP/1.1\r\nHost: local\r\nContent-Length: 1000\r\nX-Mitm-Max-Admitted-Requests: 0\r\n\r\n", path)
					response, err = http.ReadResponse(bufio.NewReader(rejected), nil)
					if err != nil {
						t.Fatal(err)
					}
					data, _ := io.ReadAll(response.Body)
					response.Body.Close()
					if response.StatusCode != 503 || !strings.Contains(string(data), "overloaded") {
						t.Fatalf("unread body was not rejected before parsing: %d %s", response.StatusCode, data)
					}
					if kind == "jsonrpc" && (!strings.Contains(string(data), `"jsonrpc":"2.0"`) || !strings.Contains(string(data), `"id":null`)) {
						t.Fatal("admission error lost JSON-RPC envelope", string(data))
					}
					fmt.Fprint(holder, "{")
					response, err = http.ReadResponse(reader, nil)
					if err == nil {
						io.Copy(io.Discard, response.Body)
						response.Body.Close()
						if response.StatusCode == 200 && kind == "fetch" {
							t.Fatal("timed-out body accepted")
						}
					}
					holder.Close()
					ingressEventually(t, func() bool { return ingressHTTPHealthy(address) })
				})
				t.Run("headers and idle timeout", func(t *testing.T) {
					address, _ := ingressStart(t, kind, binary, "-max-header-bytes", "1024", "-read-header-timeout", "150ms", "-idle-timeout", "150ms")
					conn := ingressDial(t, address)
					fmt.Fprintf(conn, "GET /health HTTP/1.1\r\nHost: local\r\nX-Pad: %s\r\n\r\n", strings.Repeat("x", 8192))
					response, err := http.ReadResponse(bufio.NewReader(conn), nil)
					if err != nil {
						t.Fatal(err)
					}
					response.Body.Close()
					if response.StatusCode != 431 {
						t.Fatal("header budget not applied", response.StatusCode)
					}
					partial := ingressDial(t, address)
					fmt.Fprint(partial, "GET /health HTTP/1.1\r\nHo")
					partial.SetReadDeadline(time.Now().Add(time.Second))
					data, err := io.ReadAll(partial)
					if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
						t.Fatal("unfinished headers outlived the read-header timeout")
					}
					if strings.Contains(string(data), "HTTP/1.1 200") {
						t.Fatal("unfinished headers accepted")
					}
					idle := ingressDial(t, address)
					fmt.Fprint(idle, "GET /health HTTP/1.1\r\nHost: local\r\n\r\n")
					response, err = http.ReadResponse(bufio.NewReader(idle), nil)
					if err != nil {
						t.Fatal(err)
					}
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					ingressClosed(t, idle)
					ingressEventually(t, func() bool { return ingressHTTPHealthy(address) })
				})
			} else {
				t.Run("RPC admission cancellation and compressed sizes", func(t *testing.T) {
					started := make(chan struct{}, 1)
					cancelled := make(chan struct{}, 1)
					var calls atomic.Int32
					target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.URL.Path == "/hold" {
							started <- struct{}{}
							<-r.Context().Done()
							cancelled <- struct{}{}
							return
						}
						io.Copy(io.Discard, r.Body)
						io.WriteString(w, "ok")
					}))
					defer target.Close()
					address, _ := ingressStart(t, kind, binary, "-max-admitted-requests", "1", "-max-request-bytes", "1024", "-max-streams-per-connection", "2")
					connection, client := newGRPCClient(t, address)
					defer connection.Close()
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := make(chan error, 1)
					go func() { _, err := client.Fetch(ctx, &pb.FetchRequest{Url: target.URL + "/hold"}); done <- err }()
					select {
					case <-started:
					case <-time.After(2 * time.Second):
						t.Fatal("target not started")
					}
					probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
					_, err := client.Health(probeCtx, &pb.HealthRequest{})
					probeCancel()
					if status.Code(err) != codes.ResourceExhausted {
						t.Fatal("RPC admission not applied to health", err)
					}
					cancel()
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Fatal("caller cancellation blocked")
					}
					select {
					case <-cancelled:
					case <-time.After(time.Second):
						t.Fatal("outbound request not canceled")
					}
					ingressEventually(t, func() bool {
						ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
						defer cancel()
						_, err := client.Health(ctx, &pb.HealthRequest{})
						return err == nil
					})
					request := &pb.FetchRequest{Url: target.URL, Method: "POST", Body: strings.Repeat("x", 900)}
					request.Body += strings.Repeat("x", 1024-proto.Size(request))
					for _, extra := range []bool{false, true, false} {
						if extra {
							request.Body += "x"
						}
						before := calls.Load()
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						response, err := client.Fetch(ctx, request, grpc.UseCompressor(gzip.Name))
						cancel()
						if extra {
							request.Body = request.Body[:len(request.Body)-1]
							if status.Code(err) != codes.ResourceExhausted || calls.Load() != before {
								t.Fatal("compressed oversized message reached target or returned wrong error", err)
							}
						} else if err != nil || !response.Ok {
							t.Fatal("exact compressed message was not supported by actual CLI", response, err)
						}
					}
				})
				t.Run("handshake deadline and advertised stream limit", func(t *testing.T) {
					address, _ := ingressStart(t, kind, binary, "-connection-timeout", "150ms", "-max-streams-per-connection", "2", "-rpc-timeout", "1s")
					raw := ingressDial(t, address)
					raw.SetDeadline(time.Now().Add(time.Second))
					_, err := io.Copy(io.Discard, raw)
					if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
						t.Fatal("handshake timeout not applied")
					}
					conn := ingressDial(t, address)
					io.WriteString(conn, http2.ClientPreface)
					frames := http2.NewFramer(conn, conn)
					frames.WriteSettings()
					for {
						frame, err := frames.ReadFrame()
						if err != nil {
							t.Fatal(err)
						}
						if settings, ok := frame.(*http2.SettingsFrame); ok && !settings.IsAck() {
							value, ok := settings.Value(http2.SettingMaxConcurrentStreams)
							if !ok || value != 2 {
								t.Fatal("stream limit not advertised", value)
							}
							frames.WriteSettingsAck()
							break
						}
					}
					conn.Close()
				})
			}
		})
	}
}
