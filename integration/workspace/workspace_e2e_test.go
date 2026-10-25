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
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	output, err := command.Output()
	if err != nil {
		t.Fatalf("read %s/go.mod: %v", directory, err)
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

	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func postJSON(t testing.TB, endpoint string, value, result any, process *testprocess.Process) {
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
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("POST %s: status=%d body=%q", endpoint, response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		t.Fatal(err)
	}
}
