//go:build policy_e2e

package policy_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"github.com/quic-go/quic-go/http3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

type h3Reply struct {
	result
	Status       int                 `json:"status"`
	HeaderValues map[string][]string `json:"header_values"`
	Trailers     map[string][]string `json:"trailers"`
}

func h3Caller(t *testing.T, kind, address string) func(context.Context, map[string]any) (h3Reply, error) {
	t.Helper()
	var client pb.FastlsServiceClient
	if kind == "grpc" {
		connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection.Close() })
		client = pb.NewFastlsServiceClient(connection)
	}
	return func(ctx context.Context, params map[string]any) (h3Reply, error) {
		data, err := json.Marshal(params)
		if err != nil {
			return h3Reply{}, err
		}
		if kind == "grpc" {
			var request pb.FetchRequest
			if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, &request); err != nil {
				return h3Reply{result: result{ErrorCode: "invalid_request"}}, nil
			}
			r, err := client.Fetch(ctx, &request)
			if err != nil {
				return h3Reply{}, err
			}
			values := map[string][]string{}
			trailers := map[string][]string{}
			for k, v := range r.HeaderValues {
				values[k] = v.Values
			}
			for k, v := range r.Trailers {
				trailers[k] = v.Values
			}
			return h3Reply{result: result{OK: r.Ok, Body: r.Body, Protocol: r.Protocol, Error: r.Error, ErrorCode: r.ErrorCode}, Status: int(r.Status), HeaderValues: values, Trailers: trailers}, nil
		}
		endpoint := "/fetch"
		if kind == "jsonrpc" {
			endpoint = "/rpc"
			data, err = json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "fetch", "params": params, "id": 1})
			if err != nil {
				return h3Reply{}, err
			}
		}
		request, err := http.NewRequestWithContext(ctx, "POST", "http://"+address+endpoint, bytes.NewReader(data))
		if err != nil {
			return h3Reply{}, err
		}
		request.Header.Set("Content-Type", "application/json")
		if headers, ok := params["inbound_headers"].(map[string]string); ok {
			for k, v := range headers {
				request.Header.Set(k, v)
			}
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return h3Reply{}, err
		}
		defer response.Body.Close()
		var reply h3Reply
		if kind == "jsonrpc" {
			var envelope struct {
				Result h3Reply         `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			err = json.NewDecoder(response.Body).Decode(&envelope)
			if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
				return h3Reply{result: result{ErrorCode: "invalid_request"}}, err
			}
			reply = envelope.Result
		} else {
			err = json.NewDecoder(response.Body).Decode(&reply)
			if response.StatusCode == 400 {
				reply.ErrorCode = "invalid_request"
			}
		}
		return reply, err
	}
}

func h3Start(t *testing.T, kind, binary string, policy any, args ...string) (func(context.Context, map[string]any) (h3Reply, error), *testprocess.Process) {
	t.Helper()
	address := testprocess.ReserveLoopbackAddress(t)
	arguments := []string{"-addr", address, "-max-inflight", "1"}
	if policy != nil {
		arguments = append(arguments, "-address-policy", policyFile(t, policy))
	}
	arguments = append(arguments, args...)
	process := testprocess.Start(t, binary, arguments...)
	call := h3Caller(t, kind, address)
	if kind == "grpc" {
		connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		client := pb.NewFastlsServiceClient(connection)
		for deadline := time.Now().Add(5 * time.Second); ; {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			_, err = client.Health(ctx, &pb.HealthRequest{})
			cancel()
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("health: %v %s", err, process.Logs())
			}
			time.Sleep(20 * time.Millisecond)
		}
	} else {
		testprocess.WaitForHTTPStatus(t, "http://"+address+"/health", 200, process)
	}
	return call, process
}

func h3Peer(t *testing.T) (string, string, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	certificate := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(certificate.Close)
	path := filepath.Join(t.TempDir(), "h3-roots.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hits, cancelled := &atomic.Int32{}, &atomic.Int32{}
	server := &http3.Server{TLSConfig: &tls.Config{Certificates: certificate.TLS.Certificates}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/hold":
			<-r.Context().Done()
			cancelled.Add(1)
		case "/slowbody":
			w.WriteHeader(200)
			fmt.Fprint(w, "start")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			cancelled.Add(1)
		case "/large":
			fmt.Fprint(w, strings.Repeat("x", 4096))
		case "/compressed":
			w.Header().Set("Content-Encoding", "gzip")
			zip := gzip.NewWriter(w)
			fmt.Fprint(zip, strings.Repeat("x", 4096))
			zip.Close()
		case "/truncated":
			w.Header().Set("Content-Length", "100")
			fmt.Fprint(w, "short")
		case "/redirect":
			http.Redirect(w, r, "http://"+r.URL.Query().Get("tcp"), 302)
		default:
			w.Header().Add("X-Repeat", "one")
			w.Header().Add("X-Repeat", "two")
			w.Header().Set("Trailer", "X-End")
			body, _ := io.ReadAll(r.Body)
			fmt.Fprint(w, r.Proto+"|"+r.Method+"|"+string(body))
			w.Header().Set("X-End", "done")
		}
	})}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(packet) }()
	t.Cleanup(func() { server.Close(); packet.Close(); <-done })
	return "https://" + packet.LocalAddr().String(), path, hits, cancelled
}

func TestServiceExplicitH3AcrossEntries(t *testing.T) {
	workspace := policyGraph(t)
	rpc, root := directories(t)
	url, ca, hits, cancelled := h3Peer(t)
	var tcpHits atomic.Int32
	tcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { tcpHits.Add(1) }))
	defer tcp.Close()
	for _, entry := range []struct{ kind, dir, pkg string }{{"fetch", policyRepository(root, "fastls-fetch"), "."}, {"jsonrpc", rpc, "./jsonrpc"}, {"grpc", rpc, "./grpc"}} {
		t.Run(entry.kind, func(t *testing.T) {
			binary := build(t, entry.dir, entry.pkg, entry.kind, workspace)
			for _, withPolicy := range []bool{false, true} {
				t.Run(fmt.Sprint("policy=", withPolicy), func(t *testing.T) {
					var policy any
					if withPolicy {
						policy = map[string]any{"allow_cidrs": []string{"127.0.0.1/32"}}
					}
					trustData, err := os.ReadFile(ca)
					if err != nil {
						t.Fatal(err)
					}
					snapshotCA := filepath.Join(t.TempDir(), "startup-roots.pem")
					if err = os.WriteFile(snapshotCA, trustData, 0600); err != nil {
						t.Fatal(err)
					}
					call, _ := h3Start(t, entry.kind, binary, policy, "-h3-ca-file", snapshotCA, "-max-response-bytes", "1024")
					if err = os.WriteFile(snapshotCA, []byte("cannot replace loaded trust"), 0600); err != nil {
						t.Fatal(err)
					}
					invoke := func(params map[string]any) h3Reply {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						r, err := call(ctx, params)
						if err != nil {
							t.Fatal(err)
						}
						return r
					}
					allowed := func() {
						r := invoke(map[string]any{"url": url, "http_version": "h3", "method": "POST", "body": "payload", "userAgent": "ordinary"})
						if !r.OK || r.Protocol != "HTTP/3.0" || r.Body != "HTTP/3.0|POST|payload" || len(r.HeaderValues["X-Repeat"]) != 2 || len(r.Trailers["X-End"]) != 1 {
							t.Fatalf("H3 response %+v", r)
						}
					}
					allowed()
					for _, params := range []map[string]any{
						{"browser": "firefox"}, {"fingerprint": map[string]string{"type": "ja4r", "value": "q-selector"}},
						{"http2_settings_string": "x"}, {"proxy": "http://user:secret@127.0.0.1:1"}, {"parent_proxy": "http://127.0.0.1:1"},
						{"trailers": map[string]any{"X-End": map[string]any{"values": []string{"x"}}}},
						{"http_version": "h4"}, {"url": tcp.URL},
					} {
						if entry.kind != "grpc" {
							if trailers, ok := params["trailers"]; ok {
								_ = trailers
								params["trailers"] = map[string][]string{"X-End": {"x"}}
							}
						}
						if _, ok := params["url"]; !ok {
							params["url"] = url
						}
						if _, ok := params["http_version"]; !ok {
							params["http_version"] = "h3"
						}
						before := hits.Load()
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						r, err := call(ctx, params)
						cancel()
						if r.OK || (err == nil && r.ErrorCode != "invalid_request") || hits.Load() != before {
							t.Fatalf("conflict reached target: %+v %v", r, err)
						}
						allowed()
					}
					if entry.kind == "fetch" {
						before := hits.Load()
						r := invoke(map[string]any{"url": url, "http_version": "h3", "inbound_headers": map[string]string{"X-Mitm-Proxy": tcp.URL}})
						if r.OK || r.ErrorCode != "invalid_request" || hits.Load() != before {
							t.Fatal("proxy header reached target")
						}
					}
					for _, path := range []string{"/large", "/compressed", "/truncated"} {
						r := invoke(map[string]any{"url": url + path, "http_version": "h3"})
						if r.OK {
							t.Fatal("invalid/oversized body accepted", path)
						}
						allowed()
					}
					beforeCancel := cancelled.Load()
					blackhole, err := net.ListenPacket("udp4", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					r := invoke(map[string]any{"url": "https://" + blackhole.LocalAddr().String(), "http_version": "h3", "timeout": 1})
					blackhole.Close()
					if r.OK || r.ErrorCode != "timeout" || tcpHits.Load() != 0 {
						t.Fatalf("unreachable UDP lost total deadline or fell back: %+v", r)
					}
					allowed()
					for _, path := range []string{"/hold", "/slowbody"} {
						r := invoke(map[string]any{"url": url + path, "http_version": "h3", "timeout": 1})
						if r.OK || r.ErrorCode != "timeout" {
							t.Fatalf("deadline %+v", r)
						}
						allowed()
					}
					ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
					_, err = call(ctx, map[string]any{"url": url + "/hold", "http_version": "h3"})
					cancel()
					if err == nil {
						t.Fatal("caller cancellation became success")
					}
					for deadline := time.Now().Add(2 * time.Second); cancelled.Load() < beforeCancel+3; {
						if time.Now().After(deadline) {
							t.Fatal("cancelled H3 handlers leaked")
						}
						time.Sleep(10 * time.Millisecond)
					}
					allowed()
					r = invoke(map[string]any{"url": url + "/redirect?tcp=" + strings.TrimPrefix(tcp.URL, "http://"), "http_version": "h3"})
					if r.OK || tcpHits.Load() != 0 {
						t.Fatal("H3 downgraded on redirect")
					}
					allowed()
					// Trust and policy are startup-only, never inherited from wire DTOs.
					r = invoke(map[string]any{"url": url, "http_version": "h3", "verifyTLS": false, "h3_ca_file": "missing", "address_policy": map[string]any{}})
					if !r.OK {
						t.Fatal("unrecognized trust fields changed frozen configuration", r)
					}
				})
			}
			call, _ := h3Start(t, entry.kind, binary, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			r, err := call(ctx, map[string]any{"url": url, "http_version": "h3", "verifyTLS": false})
			cancel()
			if err != nil || r.OK {
				t.Fatalf("untrusted certificate accepted: %+v %v", r, err)
			}
			denied, _ := h3Start(t, entry.kind, binary, map[string]any{"allow_cidrs": []string{"0.0.0.0/0", "::/0"}, "deny_cidrs": []string{"127.0.0.0/8", "::1/128"}}, "-h3-ca-file", ca)
			sink, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer sink.Close()
			_, deniedPort, _ := net.SplitHostPort(sink.LocalAddr().String())
			for _, host := range []string{"127.0.0.1", "0.0.0.0", "::", "::ffff:0.0.0.0"} {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				r, err := denied(ctx, map[string]any{"url": "https://" + net.JoinHostPort(host, deniedPort), "http_version": "h3"})
				cancel()
				if err != nil || r.OK || r.ErrorCode != "address_denied" {
					t.Fatalf("policy denied response: %+v %v", r, err)
				}
			}
			sink.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			if n, _, err := sink.ReadFromUDP(make([]byte, 2048)); err == nil || n != 0 {
				t.Fatal("policy rejection sent UDP traffic")
			}
		})
	}
}

// Installed binaries exercise actual H3 resource ownership; this is not a benchmark.
func TestServiceH3ResourceRecovery(t *testing.T) {
	if os.Getenv("FASTLS_H3_RESOURCE_SECONDS") != "300" {
		t.Skip("explicit five-minute acceptance only")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("H3 resource acceptance requires Linux proc metrics")
	}
	workspace := policyGraph(t)
	rpc, root := directories(t)
	url, ca, hits, _ := h3Peer(t)
	type owner struct {
		kind    string
		process *testprocess.Process
		call    func(context.Context, map[string]any) (h3Reply, error)
	}
	owners := []owner{}
	for _, e := range []struct{ kind, dir, pkg string }{{"fetch", policyRepository(root, "fastls-fetch"), "."}, {"jsonrpc", rpc, "./jsonrpc"}, {"grpc", rpc, "./grpc"}} {
		binary := build(t, e.dir, e.pkg, e.kind, workspace)
		call, process := h3Start(t, e.kind, binary, map[string]any{"allow_cidrs": []string{"127.0.0.1/32"}}, "-h3-ca-file", ca)
		owners = append(owners, owner{e.kind, process, call})
	}
	type sample struct {
		Seconds float64 `json:"seconds"`
		Kind    string  `json:"kind"`
		RSS     int64   `json:"rss"`
		FD      int     `json:"fds"`
	}
	samples := []sample{}
	cycles := 0
	shutdown := []map[string]any{}
	started := time.Now()
	defer func() {
		status := "passed"
		if t.Failed() {
			status = "failed"
		}
		data, _ := json.MarshalIndent(map[string]any{"status": status, "duration_seconds": time.Since(started).Seconds(), "cycles": cycles, "samples": samples, "shutdown": shutdown, "rss_growth_limit": 64 << 20, "fd_growth_limit": 24}, "", "  ")
		if evidence := os.Getenv("RELEASE_EVIDENCE"); evidence != "" {
			if err := os.WriteFile(filepath.Join(evidence, "h3-resource.json"), data, 0600); err != nil {
				t.Errorf("preserve H3 resource evidence: %v", err)
			}
		}
	}()
	last := time.Time{}
	for time.Since(started) < 300*time.Second {
		for _, o := range owners {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			r, err := o.call(ctx, map[string]any{"url": url, "http_version": "h3"})
			cancel()
			if err != nil || !r.OK {
				t.Fatalf("resource request %+v %v", r, err)
			}
			ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
			_, _ = o.call(ctx, map[string]any{"url": url + "/slowbody", "http_version": "h3"})
			cancel()
		}
		cycles++
		if time.Since(last) >= 5*time.Second {
			for _, o := range owners {
				path := fmt.Sprintf("/proc/%d", o.process.PID())
				data, err := os.ReadFile(filepath.Join(path, "status"))
				if err != nil {
					t.Fatal(err)
				}
				var rss int64
				for _, line := range strings.Split(string(data), "\n") {
					if strings.HasPrefix(line, "VmRSS:") {
						v, _ := strconv.ParseInt(strings.Fields(line)[1], 10, 64)
						rss = v * 1024
					}
				}
				fds, err := os.ReadDir(filepath.Join(path, "fd"))
				if err != nil {
					t.Fatal(err)
				}
				samples = append(samples, sample{time.Since(started).Seconds(), o.kind, rss, len(fds)})
			}
			last = time.Now()
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, o := range owners {
		var warm, end []sample
		for _, s := range samples {
			if s.Kind != o.kind {
				continue
			}
			if s.Seconds >= 15 && s.Seconds <= 45 {
				warm = append(warm, s)
			}
			if s.Seconds >= 240 {
				end = append(end, s)
			}
		}
		if len(warm) < 3 || len(end) < 3 {
			t.Fatal("insufficient resource samples")
		}
		var warmRSS, endRSS int64
		maxWarmFD, maxEndFD := 0, 0
		for _, s := range warm {
			warmRSS += s.RSS
			if s.FD > maxWarmFD {
				maxWarmFD = s.FD
			}
		}
		for _, s := range end {
			endRSS += s.RSS
			if s.FD > maxEndFD {
				maxEndFD = s.FD
			}
		}
		growth := endRSS/int64(len(end)) - warmRSS/int64(len(warm))
		if growth > 64<<20 || maxEndFD-maxWarmFD > 24 {
			t.Fatalf("H3 resource growth %s rss=%d fd=%d", o.kind, growth, maxEndFD-maxWarmFD)
		}
		t.Logf("H3 resource %s rss_growth=%d fd_growth=%d", o.kind, growth, maxEndFD-maxWarmFD)
	}
	for _, o := range owners {
		before := hits.Load()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = o.call(ctx, map[string]any{"url": url + "/hold", "http_version": "h3"})
		}()
		for deadline := time.Now().Add(3 * time.Second); hits.Load() == before; {
			if time.Now().After(deadline) {
				cancel()
				t.Fatal("shutdown request never reached H3 peer")
			}
			time.Sleep(10 * time.Millisecond)
		}
		stop := time.Now()
		if err := o.process.Signal(os.Interrupt); err != nil {
			cancel()
			t.Fatal(err)
		}
		err := o.process.Wait(8 * time.Second)
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			cancel()
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("H3 caller survived service shutdown")
		}
		cancel()
		code := 0
		if exit != nil {
			code = exit.ExitCode()
		}
		shutdown = append(shutdown, map[string]any{"entry": o.kind, "seconds": time.Since(stop).Seconds(), "exit_code": code, "bounded": true})
	}
	t.Logf("H3 resource acceptance seconds=%.3f cycles=%d", time.Since(started).Seconds(), cycles)
}
