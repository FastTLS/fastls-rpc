package responsecontract

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type cancellingEOF struct{ cancel context.CancelFunc }

func (r cancellingEOF) Read(p []byte) (int, error) {
	r.cancel()
	return copy(p, "partial"), io.EOF
}

type expiredCallbackContext struct{ context.Context }

func (expiredCallbackContext) Deadline() (time.Time, bool) { return time.Now().Add(-time.Second), true }

func TestReadContextRejectsEOFConcurrentWithCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire, display, err := ReadContext(ctx, cancellingEOF{cancel}, nil, 0)
	if !errors.Is(err, context.Canceled) || wire != nil || display != "" {
		t.Fatalf("partial body became success: %q %q %v", wire, display, err)
	}
	ctx = expiredCallbackContext{context.Background()}
	_, _, err = ReadContext(ctx, strings.NewReader("partial"), nil, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel = RequestContext(context.Background(), 1)
	defer cancel()
	wire, display, err = ReadContext(ctx, strings.NewReader("complete"), nil, 0)
	if err != nil || string(wire) != "complete" || display != "complete" {
		t.Fatal(display, err)
	}
}

func TestRequestContextKeepsEarlierDeadlineAndAvoidsOverflow(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, stop := RequestContext(parent, int(^uint(0)>>1))
	defer stop()
	actual, _ := ctx.Deadline()
	want, _ := parent.Deadline()
	if actual != want || ctx.Err() != nil {
		t.Fatal("lost parent budget", actual, want, ctx.Err())
	}
	ctx, stop = RequestContext(context.Background(), int(^uint(0)>>1))
	defer stop()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) < time.Hour {
		t.Fatal("timeout overflowed", deadline)
	}
}
