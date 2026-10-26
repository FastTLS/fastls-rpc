package responsecontract

import (
	"fmt"
	"strings"
	"testing"
)

type testAddressDenied struct{}

func (testAddressDenied) Error() string               { return "password-in-outer-wrapper" }
func (testAddressDenied) AddressPolicyReason() string { return "host_not_allowed" }

func TestAddressDeniedCapabilityAndRedaction(t *testing.T) {
	err := fmt.Errorf("https://user:password@secret.test/?token=private: %w", testAddressDenied{})
	code, phase := Code(err, "request_error")
	if code != "address_denied" || phase != "" {
		t.Fatal(code, phase)
	}
	message := ErrorMessage(err)
	if message != "address policy denied request: host_not_allowed" || strings.Contains(message, "password") || strings.Contains(message, "private") {
		t.Fatal(message)
	}
	ordinary := fmt.Errorf("ordinary")
	if ErrorMessage(ordinary) != "ordinary" {
		t.Fatal("ordinary error changed")
	}
}
