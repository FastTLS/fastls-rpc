//go:build policy_e2e

package policy_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FastTLS/fastls-rpc/internal/testprocess"
)

func TestMITMServiceUnspecifiedTargetsNeverDial(t *testing.T) {
	workspace := policyGraph(t)
	_, root := directories(t)
	binary := build(t, policyRepository(root, "fastls-mitm"), ".", "policy-mitm", workspace)
	directory := t.TempDir()
	address := testprocess.ReserveLoopbackAddress(t)
	process := testprocess.Start(t, binary, "-addr", address, "-address-policy", policyFile(t, map[string]any{
		"allow_cidrs": []string{"0.0.0.0/0", "::/0"},
		"deny_cidrs":  []string{"127.0.0.0/8", "::1/128"},
	}), "-ca-cert", filepath.Join(directory, "ca.pem"), "-ca-key", filepath.Join(directory, "key.pem"))
	testprocess.WaitForHTTPStatus(t, "http://"+address+"/", 200, process)
	var connections atomic.Int32
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "unexpected") }))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	target.Start()
	defer target.Close()
	u, _ := url.Parse(target.URL)
	for _, host := range []string{"0.0.0.0", "::", "::ffff:0.0.0.0"} {
		for _, method := range []string{"GET", "CONNECT"} {
			t.Run(method+"/"+host, func(t *testing.T) {
				conn, err := net.DialTimeout("tcp", address, 3*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				authority := net.JoinHostPort(host, u.Port())
				target := authority
				if method == "GET" {
					target = "http://" + authority + "/"
				}
				fmt.Fprintf(conn, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", method, target, authority)
				response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: method})
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusForbidden || response.Header.Get("X-Proxy-Error-Reason") != "invalid_ip" || connections.Load() != 0 {
					t.Fatalf("denial must precede CONNECT success and outbound dial: %s %v hits=%d", response.Status, response.Header, connections.Load())
				}
			})
		}
	}
}
