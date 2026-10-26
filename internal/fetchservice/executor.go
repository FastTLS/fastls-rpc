package fetchservice

import (
	"context"
	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls-rpc/internal/responsecontract"
	"github.com/FastTLS/fastls-rpc/internal/servicepolicy"
)

type Limits = responsecontract.Limits

type Config struct {
	Limits            Limits
	AddressPolicyFile string
}

// Executor owns a per-server budget shared by all requests to that server.
type Executor struct {
	client fastls.Fastls
	guard  *responsecontract.Guard
}

func NewExecutor(limits Limits) (*Executor, error) {
	return NewExecutorWithConfig(Config{Limits: limits})
}

func NewExecutorWithConfig(config Config) (*Executor, error) {
	guard, err := responsecontract.NewGuard(config.Limits)
	if err != nil {
		return nil, err
	}
	client, err := servicepolicy.Load(fastls.NewClient(), config.AddressPolicyFile)
	if err != nil {
		return nil, err
	}
	return &Executor{client: client, guard: guard}, nil
}

func (e *Executor) RequestMaximum() int64 { return e.guard.RequestMaximum() }

func (e *Executor) Fetch(ctx context.Context, params Params) (Result, error) {
	release, err := e.guard.Acquire(ctx)
	if err != nil {
		code, phase := responsecontract.Code(err, "overloaded")
		return Result{Error: err.Error(), ErrorCode: code, TimeoutPhase: phase}, nil
	}
	defer release()
	params.MaxResponseBytes = e.guard.Maximum()
	return Fetch(ctx, e.client, params)
}
