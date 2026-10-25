package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestJSONRPCCORSUsesExplicitOriginWithCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodOptions, "/rpc", nil)
	request.Header.Set("Origin", "https://app.example.test")
	recorder := httptest.NewRecorder()
	newJSONRPCRouter(NewRPCServer()).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", recorder.Code)
	}
	if origin := recorder.Header().Get("Access-Control-Allow-Origin"); origin != "https://app.example.test" {
		t.Fatalf("allow origin = %q", origin)
	}
	if credentials := recorder.Header().Get("Access-Control-Allow-Credentials"); credentials != "true" {
		t.Fatalf("allow credentials = %q", credentials)
	}
	if vary := recorder.Header().Values("Vary"); !rpcContainsHeaderToken(vary, "Origin") {
		t.Fatalf("Vary = %v", vary)
	}
	for _, token := range []string{"Access-Control-Request-Method", "Access-Control-Request-Headers"} {
		if vary := recorder.Header().Values("Vary"); !rpcContainsHeaderToken(vary, token) {
			t.Fatalf("Vary is missing %q: %v", token, vary)
		}
	}
}

func TestJSONRPCRejectsOversizedRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := `{"jsonrpc":"2.0","method":"fetch","params":{"url":"https://example.test/","body":"` +
		strings.Repeat("x", maxRPCRequestBodySize) + `"},"id":1}`
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	newJSONRPCRouter(NewRPCServer()).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, body=%q", recorder.Code, recorder.Body.String())
	}
	var response JSONRPCResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != -32600 {
		t.Fatalf("oversized response = %+v", response)
	}
	data, ok := response.Error.Data.(string)
	if !ok || !strings.Contains(data, "4194304") {
		t.Fatalf("oversized error data = %#v", response.Error.Data)
	}
}

func rpcContainsHeaderToken(values []string, token string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
