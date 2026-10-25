// Package testsupport contains local-only protocol fixtures used by end-to-end tests.
package testsupport

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type ConnectProxy struct {
	Server   *httptest.Server
	connects atomic.Int64
}

func NewConnectProxy(t testing.TB) *ConnectProxy {
	t.Helper()
	proxy := &ConnectProxy{}
	proxy.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			upstream.Close()
			http.Error(w, "hijacking unavailable", http.StatusInternalServerError)
			return
		}
		client, buffered, err := hijacker.Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		proxy.connects.Add(1)
		_, _ = fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		if err := flushBuffered(upstream, buffered); err != nil {
			client.Close()
			upstream.Close()
			return
		}
		done := make(chan struct{}, 1)
		go func() {
			_, _ = io.Copy(upstream, client)
			_ = upstream.Close()
			done <- struct{}{}
		}()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	}))
	t.Cleanup(proxy.Server.Close)
	return proxy
}

func (p *ConnectProxy) URL() string     { return p.Server.URL }
func (p *ConnectProxy) Connects() int64 { return p.connects.Load() }

func flushBuffered(destination net.Conn, buffered *bufio.ReadWriter) error {
	if buffered.Reader.Buffered() == 0 {
		return nil
	}
	_, err := io.CopyN(destination, buffered, int64(buffered.Reader.Buffered()))
	return err
}
