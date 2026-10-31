package fetchservice

import (
	"context"
	"crypto/x509"
	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls-rpc/internal/audit"
	"github.com/FastTLS/fastls-rpc/internal/h3config"
	"github.com/FastTLS/fastls-rpc/internal/responsecontract"
	"github.com/FastTLS/fastls-rpc/internal/servicepolicy"
)

type Limits = responsecontract.Limits

type Config struct {
	H3CAFile          string
	Limits            Limits
	AddressPolicyFile string
}

// Executor owns a per-server budget shared by all requests to that server.
type Executor struct {
	h3Roots *x509.CertPool
	client  fastls.Fastls
	guard   *responsecontract.Guard
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
	roots, err := h3config.LoadRoots(config.H3CAFile)
	if err != nil {
		return nil, err
	}
	return &Executor{client: client, guard: guard, h3Roots: roots}, nil
}

func (e *Executor) RequestMaximum() int64 { return e.guard.RequestMaximum() }

func (e *Executor) Fetch(ctx context.Context, params Params) (result Result, failure error) {
	defer func() {
		code, phase := result.ErrorCode, result.TimeoutPhase
		if failure != nil {
			code = "invalid_request"
		}
		audit.Result(ctx, result.Protocol, result.Status, code, phase)
	}()
	release, err := e.guard.Acquire(ctx)
	if err != nil {
		code, phase := responsecontract.Code(err, "overloaded")
		return Result{Error: err.Error(), ErrorCode: code, TimeoutPhase: phase}, nil
	}
	defer release()
	params.MaxResponseBytes = e.guard.Maximum()
	params.h3Roots = e.h3Roots
	return Fetch(ctx, e.client, params)
}
