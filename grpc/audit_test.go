package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FastTLS/fastls-rpc/internal/audit"
	"github.com/FastTLS/fastls-rpc/internal/ingress"
	"google.golang.org/grpc/codes"
)

func TestAuditRPCBeforeDecodeAndNormalCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	sink, err := audit.Open(audit.Config{File: path, Queue: 64})
	if err != nil {
		t.Fatal(err)
	}
	service := NewGRPCServer()
	service.audit = sink
	server, err := service.newTransportServerWithIngress(grpcIngressConfig{Limits: ingress.Limits{MaxAdmittedRequests: 1}})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { server.Serve(listener); close(done) }()
	defer server.Stop()
	defer sink.Close(context.Background())
	peer := rawIngressPeer(t, listener.Addr().String())
	peer.open(1, peerHeaders("Health"), nil)
	peer.barrier()
	peer.open(3, peerHeaders("Fetch"), nil)
	if code, _ := peer.result(3); code != codes.ResourceExhausted {
		t.Fatal("pre-decode rejection changed", code)
	}
	peer.reset(1)
	healthRecovers(t, clientHealth(t, listener.Addr().String()))
	fields := peerHeaders("Health")
	for i := range fields {
		if fields[i].Name == ":path" {
			fields[i].Value = strings.TrimPrefix(fields[i].Value, "/")
		}
	}
	peer.open(5, fields, []byte{0, 0, 0, 0, 0})
	if code, _ := peer.result(5); code != codes.OK {
		t.Fatal("normalized method changed", code)
	}
	peer.conn.Close()
	server.Stop()
	<-done
	for deadline := time.Now().Add(time.Second); ; {
		data, _ := os.ReadFile(path)
		if bytes.Contains(data, []byte("\"rpc_code\":\"OK\"")) && bytes.Contains(data, []byte("\"error_code\":\"overloaded\"")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("missing completion", string(data))
		}
		time.Sleep(time.Millisecond)
	}
	if err = sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	normal, rejected := false, false
	seen := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var e audit.Event
		if err = json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		if seen[e.RequestID] {
			t.Fatal("duplicate audit")
		}
		seen[e.RequestID] = true
		if e.RPCCode == "OK" {
			if e.ErrorCode != "" {
				t.Fatalf("normal RPC misclassified: %+v", e)
			}
			normal = true
		}
		if e.RPCCode == "ResourceExhausted" && e.ErrorCode == "overloaded" {
			rejected = true
		}
	}
	if !normal || !rejected {
		t.Fatalf("missing expected events: %s", data)
	}
}
