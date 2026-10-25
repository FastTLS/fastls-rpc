package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FastTLS/fastls-rpc/internal/testprocess"
)

func TestJSONRPCBinaryContract(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(append([]byte(r.Method+"|"), body...))
	}))
	defer target.Close()

	binary := testprocess.Build(t, ".", "fastls-jsonrpc")
	address := testprocess.ReserveLoopbackAddress(t)
	process := testprocess.Start(t, binary, "-addr", address)
	testprocess.WaitForHTTPStatus(t, "http://"+address+"/health", http.StatusOK, process)

	params, err := json.Marshal(FetchParams{URL: target.URL, Method: http.MethodPost, Body: "binary-rpc"})
	if err != nil {
		t.Fatal(err)
	}
	request := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "fetch",
		Params:  params,
		ID:      1,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post("http://"+address+"/rpc", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("binary JSON-RPC request failed: %v\n%s", err, process.Logs())
	}
	defer response.Body.Close()
	var result JSONRPCResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	encodedResult, err := json.Marshal(result.Result)
	if err != nil {
		t.Fatal(err)
	}
	var fetchResult FetchResult
	if err := json.Unmarshal(encodedResult, &fetchResult); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || result.Error != nil || !fetchResult.OK || fetchResult.Body != "POST|binary-rpc" {
		t.Fatalf("binary response: HTTP %d %+v", response.StatusCode, result)
	}
}
