package testprocess

import (
	"fmt"
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
	done    chan struct{}
	waitErr error
}

func (p *Process) PID() int { return p.command.Process.Pid }

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
	command.Env = os.Environ()
	if os.Getenv("GOWORK") == "" || directory != "" {
		command.Env = append(command.Env, "GOWORK=off")
	}
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
	process := &Process{command: command, logPath: logPath, logFile: logFile, done: make(chan struct{})}
	go func() {
		process.waitErr = command.Wait()
		close(process.done)
	}()
	t.Cleanup(process.Close)
	return process
}

func (p *Process) Close() {
	p.stop.Do(func() {
		if p.command.Process != nil {
			select {
			case <-p.done:
			default:
				_ = p.command.Process.Kill()
				<-p.done
			}
		}
		_ = p.logFile.Close()
	})
}

func (p *Process) Signal(signal os.Signal) error {
	select {
	case <-p.done:
		return os.ErrProcessDone
	default:
		return p.command.Process.Signal(signal)
	}
}

func (p *Process) Wait(timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return p.waitErr
	case <-timer.C:
		return fmt.Errorf("process did not exit within %s", timeout)
	}
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
