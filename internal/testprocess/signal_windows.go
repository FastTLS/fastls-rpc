//go:build windows

package testprocess

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const signalPID = "FASTLS_TEST_CONSOLE_SIGNAL_PID"

// A private console lets tests deliver a real CTRL_BREAK without signaling the
// runner, editor or other service processes. Only the short-lived helper attaches.
func init() {
	value := os.Getenv(signalPID)
	if value == "" {
		return
	}
	pid, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		os.Exit(2)
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	kernel.NewProc("FreeConsole").Call()
	if ok, _, _ := kernel.NewProc("AttachConsole").Call(uintptr(pid)); ok == 0 {
		os.Exit(3)
	}
	kernel.NewProc("SetConsoleCtrlHandler").Call(0, 1)
	if ok, _, _ := kernel.NewProc("GenerateConsoleCtrlEvent").Call(1, uintptr(pid)); ok == 0 {
		os.Exit(4)
	}
	time.Sleep(50 * time.Millisecond)
	os.Exit(0)
}

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200 | 0x00000010, HideWindow: true}
}

func signalProcess(p *os.Process, signal os.Signal) error {
	if signal != os.Interrupt {
		return fmt.Errorf("Windows test signal supports only CTRL_BREAK/Interrupt")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(exe)
	command.Env = append(os.Environ(), signalPID+"="+strconv.Itoa(p.Pid))
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
	return command.Run()
}
