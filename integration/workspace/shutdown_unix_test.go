//go:build workspace_e2e && !windows

package workspace_test

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/FastTLS/fastls-rpc/internal/testprocess"
)

func TestWorkspaceBinariesExitOnInterrupt(t *testing.T) {
	rpcRoot, workspaceRoot := workspaceDirectories(t)
	fetchBinary := testprocess.BuildInDir(t, filepath.Join(workspaceRoot, "fastls-fetch-ftransport"), ".", "fastls-fetch")
	jsonRPCBinary := testprocess.BuildInDir(t, rpcRoot, "./jsonrpc", "fastls-jsonrpc")
	grpcBinary := testprocess.BuildInDir(t, rpcRoot, "./grpc", "fastls-grpc")
	mitmBinary := testprocess.BuildInDir(t, filepath.Join(workspaceRoot, "fastls-mitm-ftransport"), ".", "fastls-mitm")

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
}
