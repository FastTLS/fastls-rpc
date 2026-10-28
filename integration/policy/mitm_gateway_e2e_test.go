//go:build policy_e2e

package policy_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FastTLS/fastls-rpc/internal/testprocess"
)

func TestMITMGatewayEnforcesPolicyForThreeRequestServices(t *testing.T) {
	rpc, root := directories(t)
	workspace := policyGraph(t)
	var connections atomic.Int32
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "gateway-ok") }))
	target.EnableHTTP2 = true
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	target.StartTLS()
	defer target.Close()
	binary := build(t, filepath.Join(root, "fastls-mitm-ftransport"), ".", "policy-mitm", workspace)
	address := testprocess.ReserveLoopbackAddress(t)
	directory := t.TempDir()
	gateway := testprocess.Start(t, binary, "-addr", address, "-address-policy", policyFile(t, map[string]any{"allow_hosts": []string{"127.0.0.1"}, "allow_cidrs": []string{"127.0.0.1/32"}}), "-ca-cert", filepath.Join(directory, "ca.pem"), "-ca-key", filepath.Join(directory, "key.pem"))
	testprocess.WaitForHTTPStatus(t, "http://"+address+"/", 200, gateway)
	for _, kind := range []string{"fetch", "jsonrpc", "grpc"} {
		t.Run(kind, func(t *testing.T) {
			dir, pkg := rpc, "./"+kind
			if kind == "fetch" {
				dir = filepath.Join(root, "fastls-fetch-ftransport")
				pkg = "."
			}
			service := build(t, dir, pkg, kind, workspace)
			// The service itself has no policy. Its parent gateway enforces
			// policy; enabling both would hit core's unsupported-proxy guard.
			call, _ := start(t, kind, service, nil)
			response := call(map[string]any{"url": target.URL, "parent_proxy": "http://" + address})
			if !response.OK || response.Body != "gateway-ok" {
				t.Fatalf("allowed gateway chain: %+v", response)
			}
			before := connections.Load()
			response = call(map[string]any{"url": strings.Replace(target.URL, "127.0.0.1", "localhost", 1), "parent_proxy": "http://" + address})
			if response.OK || connections.Load() != before {
				t.Fatalf("gateway denial bypassed: %+v", response)
			}
		})
	}
}
