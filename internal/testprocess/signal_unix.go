//go:build !windows

package testprocess

import (
	"os"
	"os/exec"
)

func configureProcess(*exec.Cmd)                          {}
func signalProcess(p *os.Process, signal os.Signal) error { return p.Signal(signal) }
