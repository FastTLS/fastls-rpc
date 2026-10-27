//go:build workspace_e2e

package workspace_test

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// Exercise the actual gRPC CLI's inbound HTTP/2 server. Benchmark-only fixes
// for Go's net/http test server must not stand in for service verification.
func assertGRPCSettingsACKs(t *testing.T, address string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var burst bytes.Buffer
	burst.WriteString(http2.ClientPreface)
	writer := http2.NewFramer(&burst, nil)
	const count = 8
	for i := 0; i < count; i++ {
		if err := writer.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: uint32(65535 + i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.Copy(connection, &burst); err != nil {
		t.Fatal(err)
	}
	reader := http2.NewFramer(connection, connection)
	acks := 0
	for acks < count {
		frame, err := reader.ReadFrame()
		if err != nil {
			t.Fatalf("received %d/%d SETTINGS ACKs: %v", acks, count, err)
		}
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if frame.IsAck() {
				acks++
			} else if err := reader.WriteSettingsAck(); err != nil {
				t.Fatal(err)
			}
		case *http2.GoAwayFrame:
			t.Fatalf("GOAWAY before all SETTINGS ACKs: %v", frame.ErrCode)
		}
	}
}
