package responsecontract

import (
	"context"
	"errors"
	"fmt"
)

// Limits is an operator setting, never a request-controlled DTO.
type Limits struct {
	// MaxRequestBytes is 0 for the compatible 4 MiB default, never unlimited.
	MaxRequestBytes  int64
	MaxResponseBytes int64
	MaxInflight      int
}

var ErrOverloaded = errors.New("service concurrency limit exceeded")

type Guard struct {
	maximum        int64
	requestMaximum int64
	slots          chan struct{}
}

func NewGuard(limits Limits) (*Guard, error) {
	if limits.MaxRequestBytes < 0 || limits.MaxRequestBytes > 64<<20 {
		return nil, fmt.Errorf("max-request-bytes must be 0..67108864 (0 selects 4 MiB)")
	}
	if limits.MaxResponseBytes < 0 || limits.MaxResponseBytes > 1<<40 {
		return nil, fmt.Errorf("max-response-bytes must be 0..1099511627776")
	}
	if limits.MaxInflight < 0 || limits.MaxInflight > 65536 {
		return nil, fmt.Errorf("max-inflight must be 0..65536")
	}
	requestMaximum := limits.MaxRequestBytes
	if requestMaximum == 0 {
		requestMaximum = 4 << 20
	}
	g := &Guard{maximum: limits.MaxResponseBytes, requestMaximum: requestMaximum}
	if limits.MaxInflight > 0 {
		g.slots = make(chan struct{}, limits.MaxInflight)
	}
	return g, nil
}
func (g *Guard) Maximum() int64        { return g.maximum }
func (g *Guard) RequestMaximum() int64 { return g.requestMaximum }
func (g *Guard) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g.slots == nil {
		return func() {}, nil
	}
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, nil
	default:
		return nil, ErrOverloaded
	}
}
