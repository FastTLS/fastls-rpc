package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FastTLS/fastls-rpc/internal/fetchservice"
)

func TestConfiguredRequestLimitIncludesTrailingAndChunkedBytes(t *testing.T) {
	payload := `{"jsonrpc":"2.0","method":"health","id":1}`
	rpc, err := NewRPCServerWithLimits(fetchservice.Limits{MaxRequestBytes: int64(len(payload))})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newJSONRPCRouter(rpc))
	defer server.Close()
	for _, chunked := range []bool{false, true} {
		for _, extra := range []int{0, 1, 8192, 0} {
			request, _ := http.NewRequest("POST", server.URL+"/rpc", strings.NewReader(payload+strings.Repeat(" ", extra)))
			if chunked {
				request.ContentLength = -1
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			want := http.StatusOK
			if extra > 0 {
				want = http.StatusRequestEntityTooLarge
			}
			if err != nil || response.StatusCode != want {
				t.Fatalf("chunked=%v extra=%d status=%d body=%s err=%v", chunked, extra, response.StatusCode, data, err)
			}
		}
	}
}
