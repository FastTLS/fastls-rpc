// Package ingress bounds inbound resources independently of outbound fetch work.
package ingress

import (
	"flag"
	"fmt"
	"net/http"
	"time"
)

type Limits struct {
	MaxConnections      int
	MaxAdmittedRequests int
	MaxHeaderBytes      int
}

func (c Limits) Validate() error {
	for _, limit := range []struct {
		name           string
		value, maximum int
	}{
		{"max-connections", c.MaxConnections, 65536},
		{"max-admitted-requests", c.MaxAdmittedRequests, 65536},
		{"max-header-bytes", c.MaxHeaderBytes, 16 << 20},
	} {
		if limit.value < 0 || limit.value > limit.maximum {
			return fmt.Errorf("%s must be 0..%d", limit.name, limit.maximum)
		}
	}
	return nil
}

func (c *Limits) BindFlags(flags *flag.FlagSet) {
	flags.IntVar(&c.MaxConnections, "max-connections", 0, "Maximum accepted TCP connections, including idle/handshake/health; 0 is unlimited")
	flags.IntVar(&c.MaxAdmittedRequests, "max-admitted-requests", 0, "Maximum admitted requests before body decoding, including health; 0 is unlimited")
	flags.IntVar(&c.MaxHeaderBytes, "max-header-bytes", 0, "Inbound header budget (HTTP/1 parser or HTTP/2 header-list accounting); 0 keeps library default, maximum 16 MiB")
}

func ValidateDuration(name string, duration time.Duration) error {
	if duration < 0 || duration > 24*time.Hour {
		return fmt.Errorf("%s must be 0..24h", name)
	}
	return nil
}

type HTTPConfig struct {
	Limits
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	IdleTimeout       time.Duration
	// OverloadHandler may format a protocol-specific error without reading the body.
	OverloadHandler http.Handler
}

func (c HTTPConfig) Validate() error {
	if err := c.Limits.Validate(); err != nil {
		return err
	}
	for _, limit := range []struct {
		name  string
		value time.Duration
	}{
		{"read-header-timeout", c.ReadHeaderTimeout}, {"read-timeout", c.ReadTimeout}, {"idle-timeout", c.IdleTimeout},
	} {
		if err := ValidateDuration(limit.name, limit.value); err != nil {
			return err
		}
	}
	return nil
}

func (c *HTTPConfig) BindFlags(flags *flag.FlagSet) {
	c.Limits.BindFlags(flags)
	flags.DurationVar(&c.ReadHeaderTimeout, "read-header-timeout", 10*time.Second, "HTTP header read timeout; 0 selects existing 10s default")
	flags.DurationVar(&c.ReadTimeout, "read-timeout", 0, "HTTP header+body read deadline per request; 0 keeps disabled")
	flags.DurationVar(&c.IdleTimeout, "idle-timeout", 0, "HTTP keepalive idle timeout; 0 uses net/http fallback to read-timeout")
}
