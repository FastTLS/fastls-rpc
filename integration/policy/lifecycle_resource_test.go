//go:build policy_e2e

package policy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FastTLS/fastls-rpc/internal/testprocess"
)

// Installed binaries exercise actual H3 resource ownership; this is not a benchmark.
func TestServiceLifecycleResourceRecovery(t *testing.T) {
	if os.Getenv("FASTLS_LIFECYCLE_RESOURCE_SECONDS") != "300" {
		t.Skip("explicit five-minute acceptance only")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("H3 resource acceptance requires Linux proc metrics")
	}
	workspace := policyGraph(t)
	rpc, root := directories(t)
	url, ca, hits, cancelled := h3Peer(t)
	var tcpHits atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tcpHits.Add(1)
		if r.URL.Path == "/slowbody" {
			fmt.Fprint(w, "start")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, r.Proto)
	})
	h1 := httptest.NewServer(handler)
	defer h1.Close()
	h2 := httptest.NewUnstartedServer(handler)
	h2.EnableHTTP2 = true
	h2.StartTLS()
	defer h2.Close()
	type owner struct {
		kind    string
		process *testprocess.Process
		call    func(context.Context, map[string]any) (h3Reply, error)
	}
	owners := []owner{}
	for _, e := range []struct{ kind, dir, pkg string }{{"fetch", policyRepository(root, "fastls-fetch"), "."}, {"jsonrpc", rpc, "./jsonrpc"}, {"grpc", rpc, "./grpc"}} {
		binary := build(t, e.dir, e.pkg, e.kind, workspace)
		call, process := h3Start(t, e.kind, binary, map[string]any{"allow_cidrs": []string{"127.0.0.1/32"}}, "-h3-ca-file", ca, "-shutdown-cancel-after", "500ms")
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
	loadSeconds := 0.0
	defer func() {
		status := "passed"
		if t.Failed() {
			status = "failed"
		}
		data, _ := json.MarshalIndent(map[string]any{"status": status, "duration_seconds": time.Since(started).Seconds(), "load_seconds": loadSeconds, "protocols": []string{"h1", "h2", "h3"}, "cycles": cycles, "samples": samples, "shutdown": shutdown, "rss_growth_limit": 64 << 20, "fd_growth_limit": 24}, "", "  ")
		if evidence := os.Getenv("RELEASE_EVIDENCE"); evidence != "" {
			if err := os.WriteFile(filepath.Join(evidence, "lifecycle-resource.json"), data, 0600); err != nil {
				t.Errorf("preserve H3 resource evidence: %v", err)
			}
		}
	}()
	last := time.Time{}
	for time.Since(started) < 300*time.Second {
		for _, o := range owners {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			protocol := cycles % 3
			target := []string{h1.URL, h2.URL, url}[protocol]
			params := map[string]any{"url": target}
			if protocol == 2 {
				params["http_version"] = "h3"
			}
			r, err := o.call(ctx, params)
			cancel()
			if err != nil || !r.OK || r.Protocol != []string{"HTTP/1.1", "HTTP/2.0", "HTTP/3.0"}[protocol] {
				t.Fatalf("resource request %+v %v", r, err)
			}
			ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
			params["url"] = target + "/slowbody"
			_, _ = o.call(ctx, params)
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
	loadSeconds = time.Since(started).Seconds()
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
		beforeCancelled := cancelled.Load()
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
		if err != nil {
			cancel()
			t.Fatal("cooperative resource exit must be zero", err)
		}
		if time.Since(stop) > 5*time.Second {
			t.Fatal("shared shutdown deadline exceeded")
		}
		for deadline := time.Now().Add(time.Second); cancelled.Load() <= beforeCancelled; {
			if time.Now().After(deadline) {
				t.Fatal("upstream not canceled at shutdown")
			}
			time.Sleep(5 * time.Millisecond)
		}

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("H3 caller survived service shutdown")
		}
		cancel()
		shutdown = append(shutdown, map[string]any{"entry": o.kind, "seconds": time.Since(stop).Seconds(), "exit_code": 0, "bounded": true, "upstream_cancelled": true})
	}
	t.Logf("H3 resource acceptance seconds=%.3f cycles=%d", time.Since(started).Seconds(), cycles)
}
