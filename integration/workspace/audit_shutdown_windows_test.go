//go:build workspace_e2e && windows

package workspace_test

import (
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"testing"
)

// Windows CLI signal delivery is not simulated with Process.Kill. Lifecycle
// cancellation and sink drain have separate Go API tests on this platform.
func stopAuditProcess(t *testing.T, p *testprocess.Process) bool { p.Close(); return false }
