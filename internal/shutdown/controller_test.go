package shutdown

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestContractIsolationAndActualCompletion(t *testing.T) {
	for _, d := range []time.Duration{-1, Total, Total + 1} {
		if _, err := New(Config{CancelAfter: d}); err == nil {
			t.Fatal("invalid interval accepted", d)
		}
	}
	c, _ := New(Config{CancelAfter: 20 * time.Millisecond})
	defer c.Close()
	other, _ := New(Config{CancelAfter: 20 * time.Millisecond})
	defer other.Close()
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, release, _ := c.Attach(parent)
	second, endSecond, _ := c.Attach(context.Background())
	defer endSecond()
	isolation, endOther, _ := other.Attach(context.Background())
	defer endOther()
	if deadline, _ := first.Deadline(); deadline.IsZero() {
		t.Fatal("lost request deadline")
	}
	cancel()
	if first.Err() == nil || second.Err() != nil {
		t.Fatal("caller cancellation crossed owners")
	}
	release()
	release()
	c.Begin()
	c.Begin()
	if err := Check(second); !errors.Is(err, context.Canceled) {
		t.Fatal("allowed late upstream work", err)
	}
	if _, _, err := c.Attach(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal("admitted after shutdown")
	}
	select {
	case <-second.Done():
	case <-time.After(time.Second):
		t.Fatal("not canceled")
	}
	if isolation.Err() != nil {
		t.Fatal("canceled another server")
	}
	wait, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := c.Wait(wait); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("released work on cancellation rather than completion", err)
	}
	endSecond()
	if err := c.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline, ok := c.Deadline()
	if !ok || time.Until(deadline) > Total {
		t.Fatal("invalid absolute budget")
	}
}

func TestHTTPBodyCancellationUnblocksAndCompletes(t *testing.T) {
	c, _ := New(Config{CancelAfter: 20 * time.Millisecond})
	defer c.Close()
	started := make(chan struct{})
	ended := make(chan struct{})
	server := &http.Server{Handler: c.HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		_, err := io.ReadAll(r.Body)
		if err == nil || r.Context().Err() == nil {
			t.Error("body read was not canceled")
		}
		close(ended)
	}))}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go server.Serve(listener)
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 999\r\n\r\nx")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler not started")
	}
	c.Begin()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt body")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}
