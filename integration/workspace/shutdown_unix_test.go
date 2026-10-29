//go:build workspace_e2e && !windows

package workspace_test

import (
	"bufio"
	"fmt"
	"golang.org/x/net/http2"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FastTLS/fastls-rpc/internal/testprocess"
)

func TestWorkspaceBinariesExitOnInterrupt(t *testing.T) {
	rpcRoot, workspaceRoot := workspaceDirectories(t)
	fetchBinary := testprocess.BuildInDir(t, workspaceRepository(workspaceRoot, "fastls-fetch"), ".", "fastls-fetch")
	jsonRPCBinary := testprocess.BuildInDir(t, rpcRoot, "./jsonrpc", "fastls-jsonrpc")
	grpcBinary := testprocess.BuildInDir(t, rpcRoot, "./grpc", "fastls-grpc")
	mitmBinary := testprocess.BuildInDir(t, workspaceRepository(workspaceRoot, "fastls-mitm"), ".", "fastls-mitm")

	fetchAddress := testprocess.ReserveLoopbackAddress(t)
	fetchProcess := testprocess.Start(t, fetchBinary, "-addr", fetchAddress)
	testprocess.WaitForHTTPStatus(t, "http://"+fetchAddress+"/health", http.StatusOK, fetchProcess)

	jsonRPCAddress := testprocess.ReserveLoopbackAddress(t)
	jsonRPCProcess := testprocess.Start(t, jsonRPCBinary, "-addr", jsonRPCAddress)
	testprocess.WaitForHTTPStatus(t, "http://"+jsonRPCAddress+"/health", http.StatusOK, jsonRPCProcess)

	grpcAddress := testprocess.ReserveLoopbackAddress(t)
	grpcProcess := testprocess.Start(t, grpcBinary, "-addr", grpcAddress)
	assertGRPCHealth(t, grpcAddress, grpcProcess)

	mitmAddress := testprocess.ReserveLoopbackAddress(t)
	caDirectory := t.TempDir()
	mitmProcess := testprocess.Start(t, mitmBinary,
		"-addr", mitmAddress,
		"-ca-cert", filepath.Join(caDirectory, "ca-cert.pem"),
		"-ca-key", filepath.Join(caDirectory, "ca-key.pem"),
	)
	testprocess.WaitForHTTPStatus(t, "http://"+mitmAddress+"/", http.StatusOK, mitmProcess)

	for _, service := range []struct {
		name    string
		address string
		process *testprocess.Process
	}{
		{name: "fastls-fetch", address: fetchAddress, process: fetchProcess},
		{name: "fastls-jsonrpc", address: jsonRPCAddress, process: jsonRPCProcess},
		{name: "fastls-grpc", address: grpcAddress, process: grpcProcess},
		{name: "fastls-mitm", address: mitmAddress, process: mitmProcess},
	} {
		t.Run(service.name, func(t *testing.T) {
			if err := service.process.Signal(os.Interrupt); err != nil {
				t.Fatalf("send interrupt: %v\n%s", err, service.process.Logs())
			}
			if err := service.process.Wait(7 * time.Second); err != nil {
				t.Fatalf("wait for graceful exit: %v\n%s", err, service.process.Logs())
			}
			connection, err := net.DialTimeout("tcp", service.address, 200*time.Millisecond)
			if err == nil {
				connection.Close()
				t.Fatalf("%s still accepts connections after exit", service.name)
			}
		})
	}
	t.Run("forced ingress cleanup preserves failure", func(t *testing.T) {
		var services []struct {
			name, address string
			process       *testprocess.Process
			connection    net.Conn
		}
		for _, input := range []struct{ name, binary, path string }{{"fetch", fetchBinary, "/fetch"}, {"jsonrpc", jsonRPCBinary, "/rpc"}, {"grpc", grpcBinary, ""}} {
			args := []string{"-max-connections", "4", "-max-admitted-requests", "1"}
			if input.name == "grpc" {
				args = append(args, "-connection-timeout", "1m")
			} else {
				args = append(args, "-read-header-timeout", "1m")
			}
			address, process := ingressStart(t, input.name, input.binary, args...)
			connection := ingressDial(t, address)
			connection.SetDeadline(time.Now().Add(10 * time.Second))
			if input.name == "grpc" {
				io.WriteString(connection, "PRI * HTTP/2.0\r\n\r\n") // Incomplete client preface.
				if _, err := http2.NewFramer(connection, connection).ReadFrame(); err != nil {
					t.Fatal(err)
				}
			} else {
				fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: local\r\nContent-Length: 1000\r\nExpect: 100-continue\r\n\r\n", input.path)
				response, err := http.ReadResponse(bufio.NewReader(connection), nil)
				if err != nil || response.StatusCode != 100 {
					t.Fatalf("pending body not admitted: %v %v", response, err)
				}
				response.Body.Close()
			}
			services = append(services, struct {
				name, address string
				process       *testprocess.Process
				connection    net.Conn
			}{input.name, address, process, connection})
		}
		// Start every grace period together rather than spending 5s per process.
		for _, service := range services {
			if err := service.process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
		}
		for _, service := range services {
			err := service.process.Wait(7 * time.Second)
			if err == nil || strings.Contains(err.Error(), "did not exit") {
				t.Fatalf("%s did not report bounded forced shutdown: %v\n%s", service.name, err, service.process.Logs())
			}
			logs := service.process.Logs()
			want := "context deadline exceeded"
			if service.name == "grpc" {
				want = "gRPC graceful shutdown exceeded"
			}
			if !strings.Contains(logs, want) {
				t.Fatalf("%s failed for a different reason: %v\n%s", service.name, err, logs)
			}
			service.connection.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := io.Copy(io.Discard, service.connection); err != nil {
				if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
					t.Fatalf("%s left an ingress socket open", service.name)
				}
			}
			if conn, err := net.DialTimeout("tcp", service.address, 200*time.Millisecond); err == nil {
				conn.Close()
				t.Fatalf("%s still accepts after shutdown", service.name)
			}
		}
	})
}
