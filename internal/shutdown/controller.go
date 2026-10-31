// Package shutdown owns opt-in cooperative request cancellation during shutdown.
package shutdown

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"sync"
	"time"
)

const Total = 5 * time.Second

type Config struct{ CancelAfter time.Duration }

func (c *Config) BindFlags(flags *flag.FlagSet) {
	flags.DurationVar(&c.CancelAfter, "shutdown-cancel-after", 0, "Opt-in cancellation after drain interval (0 disables; must be less than the shared 5s shutdown budget)")
}

type Controller struct {
	after    time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	stopping bool
	active   int
	idle     chan struct{}
	deadline time.Time
	timer    *time.Timer
}

func New(c Config) (*Controller, error) {
	if c.CancelAfter < 0 || c.CancelAfter >= Total {
		return nil, errors.New("shutdown-cancel-after must be 0 (disabled) or greater than 0 and less than 5s")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Controller{after: c.CancelAfter, ctx: ctx, cancel: cancel, idle: make(chan struct{})}, nil
}

func (c *Controller) Enabled() bool { return c != nil && c.after > 0 }

// Begin closes admission immediately. Cancellation and audit drain share Deadline.
func (c *Controller) Begin() {
	if !c.Enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopping {
		return
	}
	c.stopping = true
	c.deadline = time.Now().Add(Total)
	c.timer = time.AfterFunc(c.after, c.cancel)
	if c.active == 0 {
		close(c.idle)
	}
}

func (c *Controller) Deadline() (time.Time, bool) {
	if !c.Enabled() {
		return time.Time{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline, c.stopping
}

func (c *Controller) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()
	c.cancel()
}

type leaseKey struct{}
type ownerKey struct{}

// Check rejects work still decoding when service admission stopped.
func Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner, ok := ctx.Value(ownerKey{}).(*Controller); ok {
		owner.mu.Lock()
		defer owner.mu.Unlock()
		if owner.stopping {
			return context.Canceled
		}
	}
	return nil
}

// Attach retains the parent values/deadline and releases only after actual work.
func (c *Controller) Attach(parent context.Context) (context.Context, func(), error) {
	if !c.Enabled() {
		return parent, func() {}, nil
	}
	c.mu.Lock()
	if c.stopping || parent.Err() != nil {
		c.mu.Unlock()
		return parent, func() {}, context.Canceled
	}
	c.active++
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(c.ctx, cancel)
	var once sync.Once
	release := func() {
		once.Do(func() {
			stop()
			cancel()
			c.mu.Lock()
			defer c.mu.Unlock()
			c.active--
			if c.stopping && c.active == 0 {
				close(c.idle)
			}
		})
	}
	ctx = context.WithValue(ctx, ownerKey{}, c)
	return context.WithValue(ctx, leaseKey{}, release), release, nil
}

func Release(ctx context.Context) {
	if release, ok := ctx.Value(leaseKey{}).(func()); ok {
		release()
	}
}

func (c *Controller) Wait(ctx context.Context) error {
	if !c.Enabled() {
		return nil
	}
	select {
	case <-c.idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Controller) HTTP(next http.Handler) http.Handler {
	if !c.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, release, err := c.Attach(r.Context())
		if err != nil {
			r.Close = true
			w.Header().Set("Connection", "close")
			_ = http.NewResponseController(w).SetReadDeadline(time.Now())
			http.Error(w, "service stopping", http.StatusServiceUnavailable)
			return
		}
		defer release()
		// Canceling a context alone cannot interrupt an inbound body read.
		finished := make(chan struct{})
		stop := context.AfterFunc(c.ctx, func() {
			defer close(finished)
			<-ctx.Done()
			_ = http.NewResponseController(w).SetReadDeadline(time.Now())
		})
		defer func() {
			if !stop() {
				<-finished
			}
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
