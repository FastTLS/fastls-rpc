package ingress

import (
	"context"
	"errors"
	"sync"
)

var ErrOverloaded = errors.New("inbound request limit reached")

type Gate struct{ slots chan struct{} }

func NewGate(maximum int) (*Gate, error) {
	if err := (Limits{MaxAdmittedRequests: maximum}).Validate(); err != nil {
		return nil, err
	}
	g := &Gate{}
	if maximum > 0 {
		g.slots = make(chan struct{}, maximum)
	}
	return g, nil
}

// Acquire never waits in an HTTP/2 reader goroutine. Release is idempotent.
func (g *Gate) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g.slots == nil {
		return func() {}, nil
	}
	select {
	case g.slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-g.slots }) }, nil
	default:
		return nil, ErrOverloaded
	}
}
