package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/ingress"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func ingressServer(t *testing.T, config grpcIngressConfig) string {
	t.Helper()
	service := NewGRPCServer()
	server, err := service.newTransportServerWithIngress(config)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := ingress.NewListener(inner, config.MaxConnections)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		listener.CloseConnections()
		server.Stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("gRPC server did not stop")
		}
	})
	return listener.Addr().String()
}

type ingressPeer struct {
	t          *testing.T
	conn       net.Conn
	frames     *http2.Framer
	maxStreams uint32
	maxHeaders uint32
}

func rawIngressPeer(t *testing.T, address string) *ingressPeer {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	peer := &ingressPeer{t: t, conn: conn, frames: http2.NewFramer(conn, conn)}
	peer.frames.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		t.Fatal(err)
	}
	if err := peer.frames.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	for {
		frame := peer.read()
		if settings, ok := frame.(*http2.SettingsFrame); ok && settings.IsAck() {
			break
		}
	}
	return peer
}

func (p *ingressPeer) read() http2.Frame {
	p.t.Helper()
	frame, err := p.frames.ReadFrame()
	if err != nil {
		p.t.Fatal(err)
	}
	if settings, ok := frame.(*http2.SettingsFrame); ok && !settings.IsAck() {
		settings.ForeachSetting(func(setting http2.Setting) error {
			if setting.ID == http2.SettingMaxConcurrentStreams {
				p.maxStreams = setting.Val
			}
			if setting.ID == http2.SettingMaxHeaderListSize {
				p.maxHeaders = setting.Val
			}
			return nil
		})
		if err := p.frames.WriteSettingsAck(); err != nil {
			p.t.Fatal(err)
		}
	}
	return frame
}

func (p *ingressPeer) barrier() {
	p.t.Helper()
	nonce := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	if err := p.frames.WritePing(false, nonce); err != nil {
		p.t.Fatal(err)
	}
	for {
		if ping, ok := p.read().(*http2.PingFrame); ok && ping.IsAck() && ping.Data == nonce {
			return
		}
	}
}

func peerHeaders(method string) []hpack.HeaderField {
	return []hpack.HeaderField{{Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "http"}, {Name: ":path", Value: "/fastls.FastlsService/" + method}, {Name: ":authority", Value: "local"}, {Name: "content-type", Value: "application/grpc"}, {Name: "te", Value: "trailers"}}
}

func (p *ingressPeer) open(id uint32, headers []hpack.HeaderField, body []byte) {
	p.t.Helper()
	var encoded bytes.Buffer
	encoder := hpack.NewEncoder(&encoded)
	for _, header := range headers {
		if err := encoder.WriteField(header); err != nil {
			p.t.Fatal(err)
		}
	}
	if err := p.frames.WriteHeaders(http2.HeadersFrameParam{StreamID: id, BlockFragment: encoded.Bytes(), EndHeaders: true}); err != nil {
		p.t.Fatal(err)
	}
	if body != nil {
		if err := p.frames.WriteData(id, true, body); err != nil {
			p.t.Fatal(err)
		}
	}
}

func (p *ingressPeer) reset(id uint32) {
	p.t.Helper()
	if err := p.frames.WriteRSTStream(id, http2.ErrCodeCancel); err != nil {
		p.t.Fatal(err)
	}
	p.barrier()
}

func (p *ingressPeer) result(id uint32) (codes.Code, http2.ErrCode) {
	p.t.Helper()
	for {
		switch frame := p.read().(type) {
		case *http2.MetaHeadersFrame:
			if frame.StreamID == id {
				for _, field := range frame.Fields {
					if field.Name == "grpc-status" {
						value, err := strconv.Atoi(field.Value)
						if err != nil {
							p.t.Fatal(err)
						}
						return codes.Code(value), 0
					}
				}
			}
		case *http2.RSTStreamFrame:
			if frame.StreamID == id {
				return codes.Unknown, frame.ErrCode
			}
		case *http2.GoAwayFrame:
			p.t.Fatalf("connection rejected: %v", frame.ErrCode)
		}
	}
}

func clientHealth(t *testing.T, address string) func() error {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	client := pb.NewFastlsServiceClient(connection)
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := client.Health(ctx, &pb.HealthRequest{})
		return err
	}
}
func healthRecovers(t *testing.T, health func() error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if err = health(); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("health permit not recovered", err)
}

func TestIngressGRPCRejectsBeforeMessageAndReleasesOnReset(t *testing.T) {
	address := ingressServer(t, grpcIngressConfig{Limits: ingress.Limits{MaxAdmittedRequests: 1}, MaxStreamsPerConnection: 8})
	peer := rawIngressPeer(t, address)
	peer.open(1, peerHeaders("Health"), nil)
	peer.barrier() // No message prefix or body sent.
	headers := append(peerHeaders("Fetch"), hpack.HeaderField{Name: "grpc-encoding", Value: "gzip"})
	peer.open(3, headers, nil)
	if code, reset := peer.result(3); code != codes.ResourceExhausted || reset != 0 {
		t.Fatalf("body was needed before rejecting stream: code=%v reset=%v", code, reset)
	}
	health := clientHealth(t, address)
	if err := health(); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("admission was not global across connections: %v", err)
	}
	peer.reset(1)
	healthRecovers(t, health)
	// A decoding failure, including gzip failure, also returns its admission slot.
	peer.open(5, headers, []byte{1, 0, 0, 0, 1, 255})
	if code, _ := peer.result(5); code != codes.Internal {
		t.Fatalf("wrong malformed compressed-message status: %v", code)
	}
	healthRecovers(t, health)
	peer.open(7, peerHeaders("Health"), nil)
	peer.barrier()
	peer.conn.Close()
	healthRecovers(t, health)
	for i := 0; i < 50; i++ {
		if err := health(); err != nil {
			t.Fatalf("sequential RPC spuriously rejected: %v", err)
		}
	}
}

func TestIngressGRPCPerConnectionStreamsAndExactHeaderBudget(t *testing.T) {
	t.Run("streams", func(t *testing.T) {
		address := ingressServer(t, grpcIngressConfig{MaxStreamsPerConnection: 2})
		peer := rawIngressPeer(t, address)
		if peer.maxStreams != 2 {
			t.Fatalf("stream limit not advertised: %d", peer.maxStreams)
		}
		peer.open(1, peerHeaders("Health"), nil)
		peer.barrier()
		peer.open(3, peerHeaders("Health"), nil)
		peer.barrier()
		peer.open(5, peerHeaders("Health"), nil)
		if _, reset := peer.result(5); reset != http2.ErrCodeRefusedStream {
			t.Fatalf("N+1 stream was not refused: %v", reset)
		}
		peer.reset(1)
		peer.open(7, peerHeaders("Health"), []byte{0, 0, 0, 0, 0})
		if code, reset := peer.result(7); code != codes.OK || reset != 0 {
			t.Fatalf("stream permit not returned: %v %v", code, reset)
		}
		peer.reset(3)
	})
	t.Run("headers", func(t *testing.T) {
		const limit = 512
		address := ingressServer(t, grpcIngressConfig{Limits: ingress.Limits{MaxHeaderBytes: limit}})
		peer := rawIngressPeer(t, address)
		if peer.maxHeaders != limit {
			t.Fatal("header-list limit not advertised", peer.maxHeaders)
		}
		fields := peerHeaders("Health")
		size := 0
		for _, field := range fields {
			size += int(field.Size())
		}
		padding := limit - size - len("x-pad") - 32
		if padding < 0 {
			t.Fatal("invalid test header budget")
		}
		fields = append(fields, hpack.HeaderField{Name: "x-pad", Value: strings.Repeat("x", padding)})
		peer.open(1, fields, []byte{0, 0, 0, 0, 0})
		if code, reset := peer.result(1); code != codes.OK || reset != 0 {
			t.Fatalf("exact header budget failed: %v %v", code, reset)
		}
		fields[len(fields)-1].Value += "x"
		peer.open(3, fields, nil)
		if _, reset := peer.result(3); reset == 0 {
			t.Fatal("oversized header list accepted")
		}
		peer.open(5, peerHeaders("Health"), []byte{0, 0, 0, 0, 0})
		if code, reset := peer.result(5); code != codes.OK || reset != 0 {
			t.Fatalf("header rejection poisoned connection: %v %v", code, reset)
		}
	})
}

func TestIngressGRPCHandshakeAndMessageDeadlines(t *testing.T) {
	t.Run("handshake", func(t *testing.T) {
		address := ingressServer(t, grpcIngressConfig{Limits: ingress.Limits{MaxConnections: 1}, ConnectionTimeout: 120 * time.Millisecond})
		conn, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		// The server may send its SETTINGS first; the connection must then end.
		if _, err := io.Copy(io.Discard, conn); err == nil {
		} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("unfinished handshake was not closed")
		}
		healthRecovers(t, clientHealth(t, address))
	})
	t.Run("message", func(t *testing.T) {
		address := ingressServer(t, grpcIngressConfig{Limits: ingress.Limits{MaxAdmittedRequests: 1}, RPCTimeout: 250 * time.Millisecond})
		peer := rawIngressPeer(t, address)
		peer.open(1, peerHeaders("Health"), nil)
		if code, reset := peer.result(1); code != codes.DeadlineExceeded || reset != 0 {
			t.Fatalf("unread message not canceled by server deadline: %v %v", code, reset)
		}
		healthRecovers(t, clientHealth(t, address))
	})
	t.Run("outbound cancellation", func(t *testing.T) {
		var calls atomic.Int32
		cancelled := make(chan struct{}, 1)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			<-r.Context().Done()
			cancelled <- struct{}{}
		}))
		defer target.Close()
		address := ingressServer(t, grpcIngressConfig{Limits: ingress.Limits{MaxAdmittedRequests: 1}, RPCTimeout: 250 * time.Millisecond})
		connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err = pb.NewFastlsServiceClient(connection).Fetch(ctx, &pb.FetchRequest{Url: target.URL})
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("server deadline not reported: %v", err)
		}
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("outbound context not canceled")
		}
		if calls.Load() != 1 {
			t.Fatal("wrong target count", calls.Load())
		}
		healthRecovers(t, clientHealth(t, address))
	})
}

func TestIngressGRPCInvalidStartup(t *testing.T) {
	for _, config := range []grpcIngressConfig{{MaxStreamsPerConnection: -1}, {MaxStreamsPerConnection: 65537}, {ConnectionTimeout: -1}, {RPCTimeout: 25 * time.Hour}} {
		if _, err := NewGRPCServer().newTransportServerWithIngress(config); err == nil {
			t.Fatal(fmt.Sprintf("invalid gRPC startup accepted: %+v", config))
		}
	}
}
