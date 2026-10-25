package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testsupport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCFetchWiresHTTP2Settings(t *testing.T) {
	_, err := NewGRPCServer().Fetch(context.Background(), &pb.FetchRequest{
		Url:                 "https://example.test/",
		Http2SettingsString: "not-settings",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status code = %v, want %v (err=%v)", status.Code(err), codes.InvalidArgument, err)
	}
	_, err = NewGRPCServer().Fetch(context.Background(), &pb.FetchRequest{
		Url: "https://example.test/", Fingerprint: &pb.Fingerprint{},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty fingerprint status = %v, want %v (err=%v)", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestGRPCFetchParentProxyPrecedence(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "grpc-parent-proxy")
	}))
	defer target.Close()
	proxy := testsupport.NewConnectProxy(t)

	response, err := NewGRPCServer().Fetch(context.Background(), &pb.FetchRequest{
		Url: target.URL + "/probe", Proxy: "http://127.0.0.1:1", ParentProxy: proxy.URL(),
	})
	if err != nil || !response.Ok || response.Body != "grpc-parent-proxy" || proxy.Connects() != 1 {
		t.Fatalf("unexpected response: response=%+v err=%v", response, err)
	}
}
