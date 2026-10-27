//go:build workspace_e2e && !windows

package workspace_test

import (
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"os"
	"testing"
	"time"
)

func stopAuditProcess(t *testing.T, p *testprocess.Process) bool {
	t.Helper()
	if err := p.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(8 * time.Second); err != nil {
		t.Fatalf("audit shutdown: %v\n%s", err, p.Logs())
	}
	return true
}
