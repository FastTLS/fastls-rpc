package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FastTLS/fastls-rpc/internal/serverlifecycle"
	"github.com/FastTLS/fastls-rpc/internal/testsupport"
	"github.com/gin-gonic/gin"
)

func TestJSONRPCServerStopsOnCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{Handler: newJSONRPCRouter(NewRPCServer())}
	done := make(chan error, 1)
	go func() { done <- serverlifecycle.ServeHTTP(ctx, listener, server) }()

	response, err := http.Get("http://" + listener.Addr().String() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("JSON-RPC server did not stop after cancellation")
	}
}

func TestJSONRPCFetchWiresHTTP2Settings(t *testing.T) {
	params := json.RawMessage(`{"url":"https://example.test/","http2_settings_string":"not-settings"}`)
	_, rpcErr := NewRPCServer().handleFetch(context.Background(), params)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("RPC error = %+v, want invalid params", rpcErr)
	}
	params = json.RawMessage(`{"url":"https://example.test/","fingerprint":{}}`)
	_, rpcErr = NewRPCServer().handleFetch(context.Background(), params)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("empty fingerprint RPC error = %+v, want invalid params", rpcErr)
	}
}

func TestJSONRPCFetchParentProxyPrecedence(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "jsonrpc-parent-proxy")
	}))
	defer target.Close()
	proxy := testsupport.NewConnectProxy(t)

	params, err := json.Marshal(FetchParams{
		URL: target.URL + "/probe", Proxy: "http://127.0.0.1:1", ParentProxy: proxy.URL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, rpcErr := NewRPCServer().handleFetch(context.Background(), params)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	fetchResult := result.(FetchResult)
	if !fetchResult.OK || fetchResult.Body != "jsonrpc-parent-proxy" || proxy.Connects() != 1 {
		t.Fatalf("unexpected result: %+v", fetchResult)
	}
}
