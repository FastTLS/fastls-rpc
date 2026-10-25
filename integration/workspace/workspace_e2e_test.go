//go:build workspace_e2e

package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestWorkspaceDependencyAlignment(t *testing.T) {
	rpcRoot, workspaceRoot := workspaceDirectories(t)
	repositories := []struct {
		name       string
		root       string
		modulePath string
	}{
		{name: "fastls-fetch", root: filepath.Join(workspaceRoot, "fastls-fetch-ftransport"), modulePath: "github.com/FastTLS/fastls-fetch"},
		{name: "fastls-rpc", root: rpcRoot, modulePath: "github.com/FastTLS/fastls-rpc"},
		{name: "fastls-mitm", root: filepath.Join(workspaceRoot, "fastls-mitm-ftransport"), modulePath: "github.com/FastTLS/fastls-mitm"},
	}
	sharedDependencies := []string{
		"github.com/FastTLS/fastls",
		"github.com/FastTLS/fastls/profile",
		"github.com/FastTLS/ftransport",
		"github.com/FastTLS/imitate",
	}

	expectedVersions := make(map[string]string, len(sharedDependencies))
	for _, repository := range repositories {
		manifest := readGoMod(t, repository.root)
		if manifest.Module.Path != repository.modulePath {
			t.Errorf("%s module path: got %q, want %q", repository.name, manifest.Module.Path, repository.modulePath)
		}
		if len(manifest.Replace) != 0 {
			t.Errorf("%s contains replace directives; workspace compatibility must use publishable dependencies", repository.name)
		}

		requirements := make(map[string]string, len(manifest.Require))
		for _, requirement := range manifest.Require {
			requirements[requirement.Path] = requirement.Version
		}
		for _, dependency := range sharedDependencies {
			version, ok := requirements[dependency]
			if !ok {
				t.Errorf("%s does not require %s", repository.name, dependency)
				continue
			}
			if expectedVersions[dependency] == "" {
				expectedVersions[dependency] = version
				continue
			}
			if version != expectedVersions[dependency] {
				t.Errorf("%s requires %s@%s, want %s", repository.name, dependency, version, expectedVersions[dependency])
			}
		}
	}
}

type goModManifest struct {
	Module struct {
		Path string
	}
	Require []struct {
		Path    string
		Version string
	}
	Replace []json.RawMessage
}

func readGoMod(t testing.TB, directory string) goModManifest {
	t.Helper()
	command := exec.Command("go", "mod", "edit", "-json")
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read %s/go.mod: %v\n%s", directory, err, output)
	}
	var manifest goModManifest
	if err := json.Unmarshal(output, &manifest); err != nil {
		t.Fatalf("decode %s/go.mod: %v", directory, err)
	}
	return manifest
}

func TestWorkspaceBinariesThroughMITM(t *testing.T) {
	rpcRoot, workspaceRoot := workspaceDirectories(t)
	fetchRoot := filepath.Join(workspaceRoot, "fastls-fetch-ftransport")
	mitmRoot := filepath.Join(workspaceRoot, "fastls-mitm-ftransport")

	fetchBinary := testprocess.BuildInDir(t, fetchRoot, ".", "fastls-fetch")
	jsonRPCBinary := testprocess.BuildInDir(t, rpcRoot, "./jsonrpc", "fastls-jsonrpc")
	grpcBinary := testprocess.BuildInDir(t, rpcRoot, "./grpc", "fastls-grpc")
	mitmBinary := testprocess.BuildInDir(t, mitmRoot, ".", "fastls-mitm")

	var targetRequests atomic.Int64
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, r.Proto+"|"+r.Method+"|"+string(body))
	}))
	target.EnableHTTP2 = true
	target.StartTLS()
	defer target.Close()

	mitmAddress := testprocess.ReserveLoopbackAddress(t)
	caDirectory := t.TempDir()
	mitmProcess := testprocess.Start(t, mitmBinary,
		"-addr", mitmAddress,
		"-ca-cert", filepath.Join(caDirectory, "ca-cert.pem"),
		"-ca-key", filepath.Join(caDirectory, "ca-key.pem"),
	)
	testprocess.WaitForHTTPStatus(t, "http://"+mitmAddress+"/", http.StatusOK, mitmProcess)
	parentProxy := "http://" + mitmAddress

	fetchAddress := testprocess.ReserveLoopbackAddress(t)
	fetchProcess := testprocess.Start(t, fetchBinary, "-addr", fetchAddress)
	testprocess.WaitForHTTPStatus(t, "http://"+fetchAddress+"/health", http.StatusOK, fetchProcess)
	assertFetchThroughMITM(t, "http://"+fetchAddress+"/fetch", target.URL, parentProxy, fetchProcess)

	jsonRPCAddress := testprocess.ReserveLoopbackAddress(t)
	jsonRPCProcess := testprocess.Start(t, jsonRPCBinary, "-addr", jsonRPCAddress)
	testprocess.WaitForHTTPStatus(t, "http://"+jsonRPCAddress+"/health", http.StatusOK, jsonRPCProcess)
	assertJSONRPCThroughMITM(t, "http://"+jsonRPCAddress+"/rpc", target.URL, parentProxy, jsonRPCProcess)

	grpcAddress := testprocess.ReserveLoopbackAddress(t)
	grpcProcess := testprocess.Start(t, grpcBinary, "-addr", grpcAddress)
	assertGRPCThroughMITM(t, grpcAddress, target.URL, parentProxy, grpcProcess)

	if got := targetRequests.Load(); got != 3 {
		t.Fatalf("successful target requests = %d, want 3", got)
	}

	t.Run("invalid configuration is rejected", func(t *testing.T) {
		assertFetchRejectsInvalidConfiguration(t, "http://"+fetchAddress+"/fetch", fetchProcess)
		assertJSONRPCRejectsInvalidConfiguration(t, "http://"+jsonRPCAddress+"/rpc", jsonRPCProcess)
		assertGRPCRejectsInvalidConfiguration(t, grpcAddress, grpcProcess)
	})

	t.Run("oversized requests are rejected", func(t *testing.T) {
		body := strings.Repeat("x", (4<<20)+1)
		assertFetchRejectsOversizedRequest(t, "http://"+fetchAddress+"/fetch", body, fetchProcess)
		assertJSONRPCRejectsOversizedRequest(t, "http://"+jsonRPCAddress+"/rpc", body, jsonRPCProcess)
		assertGRPCRejectsOversizedRequest(t, grpcAddress, body, grpcProcess)
	})

	t.Run("rejected proxy never falls back to direct", func(t *testing.T) {
		var proxyRequests atomic.Int64
		var nonConnectRequests atomic.Int64
		rejectingProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proxyRequests.Add(1)
			if r.Method != http.MethodConnect {
				nonConnectRequests.Add(1)
			}
			http.Error(w, "proxy unavailable", http.StatusBadGateway)
		}))
		defer rejectingProxy.Close()

		assertFetchProxyFailure(t, "http://"+fetchAddress+"/fetch", target.URL, rejectingProxy.URL, fetchProcess)
		assertJSONRPCProxyFailure(t, "http://"+jsonRPCAddress+"/rpc", target.URL, rejectingProxy.URL, jsonRPCProcess)
		assertGRPCProxyFailure(t, grpcAddress, target.URL, rejectingProxy.URL, grpcProcess)
		if got := proxyRequests.Load(); got != 3 {
			t.Fatalf("rejecting proxy requests = %d, want 3", got)
		}
		if got := nonConnectRequests.Load(); got != 0 {
			t.Fatalf("rejecting proxy received %d non-CONNECT requests", got)
		}
		if got := targetRequests.Load(); got != 3 {
			t.Fatalf("target received %d requests after proxy failures; direct fallback occurred", got)
		}
	})

	testprocess.WaitForHTTPStatus(t, "http://"+fetchAddress+"/health", http.StatusOK, fetchProcess)
	testprocess.WaitForHTTPStatus(t, "http://"+jsonRPCAddress+"/health", http.StatusOK, jsonRPCProcess)
	assertGRPCHealth(t, grpcAddress, grpcProcess)
}

func workspaceDirectories(t testing.TB) (string, string) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate workspace integration test")
	}
	rpcRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	return rpcRoot, filepath.Dir(rpcRoot)
}

func assertFetchThroughMITM(t testing.TB, endpoint, target, parentProxy string, process *testprocess.Process) {
	t.Helper()
	payload := map[string]any{
		"url": target, "method": http.MethodPost, "body": "fetch-via-mitm", "parent_proxy": parentProxy,
	}
	var response struct {
		OK    bool   `json:"ok"`
		Body  string `json:"body"`
		Error string `json:"error"`
	}
	postJSON(t, endpoint, payload, &response, process)
	if !response.OK || response.Body != "HTTP/2.0|POST|fetch-via-mitm" {
		t.Fatalf("fetch through MITM: %+v", response)
	}
}

func assertJSONRPCThroughMITM(t testing.TB, endpoint, target, parentProxy string, process *testprocess.Process) {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "fetch",
		"params": map[string]any{
			"url": target, "method": http.MethodPost, "body": "jsonrpc-via-mitm", "parent_proxy": parentProxy,
		},
		"id": 1,
	}
	var response struct {
		Result struct {
			OK    bool   `json:"ok"`
			Body  string `json:"body"`
			Error string `json:"error"`
		} `json:"result"`
		Error any `json:"error"`
	}
	postJSON(t, endpoint, payload, &response, process)
	if response.Error != nil || !response.Result.OK || response.Result.Body != "HTTP/2.0|POST|jsonrpc-via-mitm" {
		t.Fatalf("JSON-RPC through MITM: %+v", response)
	}
}

func assertGRPCThroughMITM(t testing.TB, address, target, parentProxy string, process *testprocess.Process) {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := pb.NewFastlsServiceClient(connection)
	var response *pb.FetchResponse
	var lastErr error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		response, lastErr = client.Fetch(ctx, &pb.FetchRequest{
			Url: target, Method: http.MethodPost, Body: "grpc-via-mitm", ParentProxy: parentProxy,
		})
		cancel()
		if lastErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("gRPC through MITM failed: %v\n%s", lastErr, process.Logs())
	}
	if !response.Ok || response.Body != "HTTP/2.0|POST|grpc-via-mitm" {
		t.Fatalf("gRPC through MITM: %+v", response)
	}
}

func assertFetchRejectsInvalidConfiguration(t testing.TB, endpoint string, process *testprocess.Process) {
	t.Helper()
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	postJSONStatus(t, endpoint, map[string]any{"url": "https://invalid.test", "timeout": -1}, &response, http.StatusBadRequest, process)
	if response.OK || response.Error == "" {
		t.Fatalf("fetch invalid configuration response: %+v", response)
	}
}

func assertJSONRPCRejectsInvalidConfiguration(t testing.TB, endpoint string, process *testprocess.Process) {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "fetch",
		"params":  map[string]any{"url": "https://invalid.test", "timeout": -1},
		"id":      1,
	}
	var response struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    any    `json:"data"`
		} `json:"error"`
	}
	postJSON(t, endpoint, payload, &response, process)
	if response.Error == nil || response.Error.Code != -32602 {
		t.Fatalf("JSON-RPC invalid configuration response: %+v", response)
	}
}

func assertGRPCRejectsInvalidConfiguration(t testing.TB, address string, process *testprocess.Process) {
	t.Helper()
	connection, client := newGRPCClient(t, address)
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Fetch(ctx, &pb.FetchRequest{Url: "https://invalid.test", Timeout: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("gRPC invalid configuration error = %v, want InvalidArgument\n%s", err, process.Logs())
	}
}

func assertFetchRejectsOversizedRequest(t testing.TB, endpoint, body string, process *testprocess.Process) {
	t.Helper()
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	postJSONStatus(t, endpoint, map[string]any{
		"url": "https://oversized.invalid", "body": body,
	}, &response, http.StatusRequestEntityTooLarge, process)
	if response.OK || response.Error == "" {
		t.Fatalf("fetch oversized request response: %+v", response)
	}
}

func assertJSONRPCRejectsOversizedRequest(t testing.TB, endpoint, body string, process *testprocess.Process) {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "fetch",
		"params":  map[string]any{"url": "https://oversized.invalid", "body": body},
		"id":      1,
	}
	var response struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    any    `json:"data"`
		} `json:"error"`
	}
	postJSONStatus(t, endpoint, payload, &response, http.StatusRequestEntityTooLarge, process)
	if response.Error == nil || response.Error.Code != -32600 {
		t.Fatalf("JSON-RPC oversized request response: %+v", response)
	}
}

func assertGRPCRejectsOversizedRequest(t testing.TB, address, body string, process *testprocess.Process) {
	t.Helper()
	connection, client := newGRPCClient(t, address)
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Fetch(ctx, &pb.FetchRequest{Url: "https://oversized.invalid", Body: body})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("gRPC oversized request error = %v, want ResourceExhausted\n%s", err, process.Logs())
	}
}

func assertFetchProxyFailure(t testing.TB, endpoint, target, parentProxy string, process *testprocess.Process) {
	t.Helper()
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	postJSON(t, endpoint, map[string]any{
		"url": target, "parent_proxy": parentProxy, "timeout": 1,
	}, &response, process)
	if response.OK || response.Error == "" {
		t.Fatalf("fetch rejected proxy response: %+v", response)
	}
}

func assertJSONRPCProxyFailure(t testing.TB, endpoint, target, parentProxy string, process *testprocess.Process) {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "fetch",
		"params": map[string]any{
			"url": target, "parent_proxy": parentProxy, "timeout": 1,
		},
		"id": 1,
	}
	var response struct {
		Result struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"result"`
		Error any `json:"error"`
	}
	postJSON(t, endpoint, payload, &response, process)
	if response.Error != nil || response.Result.OK || response.Result.Error == "" {
		t.Fatalf("JSON-RPC rejected proxy response: %+v", response)
	}
}

func assertGRPCProxyFailure(t testing.TB, address, target, parentProxy string, process *testprocess.Process) {
	t.Helper()
	connection, client := newGRPCClient(t, address)
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := client.Fetch(ctx, &pb.FetchRequest{
		Url: target, ParentProxy: parentProxy, Timeout: 1,
	})
	if err != nil {
		t.Fatalf("gRPC rejected proxy call failed: %v\n%s", err, process.Logs())
	}
	if response.Ok || response.Error == "" {
		t.Fatalf("gRPC rejected proxy response: %+v", response)
	}
}

func assertGRPCHealth(t testing.TB, address string, process *testprocess.Process) {
	t.Helper()
	connection, client := newGRPCClient(t, address)
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := client.Health(ctx, &pb.HealthRequest{})
	if err != nil || response.Status != "ok" {
		t.Fatalf("gRPC health after failures: response=%+v err=%v\n%s", response, err, process.Logs())
	}
}

func newGRPCClient(t testing.TB, address string) (*grpc.ClientConn, pb.FastlsServiceClient) {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	return connection, pb.NewFastlsServiceClient(connection)
}

func postJSON(t testing.TB, endpoint string, value, result any, process *testprocess.Process) {
	postJSONStatus(t, endpoint, value, result, http.StatusOK, process)
}

func postJSONStatus(t testing.TB, endpoint string, value, result any, expectedStatus int, process *testprocess.Process) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(endpoint, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST %s: %v\n%s", endpoint, err, process.Logs())
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("POST %s: status=%d body=%q", endpoint, response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		t.Fatal(err)
	}
}
