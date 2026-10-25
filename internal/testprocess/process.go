package testprocess

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

type Process struct {
	command *exec.Cmd
	logPath string
	logFile *os.File
	stop    sync.Once
}

func Build(t testing.TB, packagePath, name string) string {
	return BuildInDir(t, "", packagePath, name)
}

func BuildInDir(t testing.TB, directory, packagePath, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-trimpath", "-o", path, packagePath)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build test binary: %v\n%s", err, output)
	}
	return path
}

func ReserveLoopbackAddress(t testing.TB) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func Start(t testing.TB, binary string, arguments ...string) *Process {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "process.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, arguments...)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	process := &Process{command: command, logPath: logPath, logFile: logFile}
	t.Cleanup(process.Close)
	return process
}

func (p *Process) Close() {
	p.stop.Do(func() {
		if p.command.Process != nil {
			_ = p.command.Process.Kill()
			_, _ = p.command.Process.Wait()
		}
		_ = p.logFile.Close()
	})
}

func (p *Process) Logs() string {
	p.Close()
	data, _ := os.ReadFile(p.logPath)
	return string(data)
}

func WaitForHTTPStatus(t testing.TB, endpoint string, expected int, process *Process) {
	t.Helper()
	var lastStatus int
	var lastErr error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		response, err := http.Get(endpoint)
		if err == nil {
			response.Body.Close()
			lastStatus = response.StatusCode
			if response.StatusCode == expected {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("service did not become ready: status=%d err=%v\n%s", lastStatus, lastErr, process.Logs())
}
