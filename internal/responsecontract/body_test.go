package responsecontract

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"errors"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"io"
	"net/http"
	"testing"
)

func FuzzBoundedResponseDecode(f *testing.F) {
	f.Add([]byte("plain"), "")
	f.Add([]byte("invalid"), "gzip")
	f.Add([]byte{0x28, 0xb5, 0x2f, 0xfd}, "zstd")
	f.Fuzz(func(t *testing.T, data []byte, coding string) {
		if len(data) > 8192 || len(coding) > 256 {
			return
		}
		snapshot := append([]byte(nil), data...)
		wire, _, err := Read(bytes.NewReader(data), http.Header{"Content-Encoding": {coding}}, 1024)
		if err == nil && len(wire) > 1024 {
			t.Fatal("encoded body exceeded bound")
		}
		if !bytes.Equal(data, snapshot) {
			t.Fatal("decoder mutated caller data")
		}
	})
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestWireBodyAndDecodeLimits(t *testing.T) {
	payload := bytes.Repeat([]byte("payload"), 1024)
	encoded := gzipBytes(t, payload)
	wire, text, err := Read(bytes.NewReader(encoded), http.Header{"Content-Encoding": {"gzip"}}, int64(len(payload)))
	if err != nil || !bytes.Equal(wire, encoded) || text != string(payload) {
		t.Fatalf("decoded body mismatch: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(Base64(wire))
	if err != nil || !bytes.Equal(decoded, encoded) {
		t.Fatal("wire body was not preserved")
	}
	for _, limit := range []int64{int64(len(encoded) - 1), int64(len(payload) - 1)} {
		_, _, err = Read(bytes.NewReader(encoded), http.Header{"Content-Encoding": {"gzip"}}, limit)
		code, _ := Code(err, "read_error")
		if code != "response_too_large" {
			t.Fatalf("limit %d: %v (%s)", limit, err, code)
		}
	}
	double := gzipBytes(t, encoded)
	_, text, err = Read(bytes.NewReader(double), http.Header{"Content-Encoding": {"gzip, gzip"}}, int64(len(payload)))
	if err != nil || text != string(payload) {
		t.Fatalf("stacked encoding: %v", err)
	}
	_, _, err = Read(bytes.NewReader(encoded[:len(encoded)-3]), http.Header{"Content-Encoding": {"gzip"}}, 0)
	if code, _ := Code(err, "read_error"); code != "decode_error" {
		t.Fatalf("truncation was hidden: %v", err)
	}
	_, _, err = Read(bytes.NewReader(encoded), http.Header{"Content-Encoding": {"unknown"}}, 0)
	if code, _ := Code(err, "read_error"); code != "decode_error" {
		t.Fatalf("unsupported encoding: %v", err)
	}
}

func TestBinaryWireBodyAndExactLimit(t *testing.T) {
	data := []byte{0, 255, 128, 1}
	wire, text, err := Read(bytes.NewReader(data), http.Header{"Content-Type": {"application/pdf"}}, int64(len(data)))
	if err != nil || !bytes.Equal(wire, data) || text != base64.StdEncoding.EncodeToString(data) {
		t.Fatal("binary response changed")
	}
	_, _, err = Read(bytes.NewReader(data), http.Header{}, int64(len(data)-1))
	var limit *LimitError
	if !errors.As(err, &limit) {
		t.Fatalf("expected limit error: %v", err)
	}
}

func TestAllSupportedCodecsRespectDecodedLimit(t *testing.T) {
	payload := bytes.Repeat([]byte("codec-payload"), 1024)
	for _, kind := range []string{"deflate-zlib", "deflate-raw", "br", "zstd"} {
		t.Run(kind, func(t *testing.T) {
			var encoded bytes.Buffer
			var writer io.WriteCloser
			coding := kind
			switch kind {
			case "deflate-zlib":
				coding = "deflate"
				writer = zlib.NewWriter(&encoded)
			case "deflate-raw":
				coding = "deflate"
				var err error
				writer, err = flate.NewWriter(&encoded, flate.BestSpeed)
				if err != nil {
					t.Fatal(err)
				}
			case "br":
				writer = brotli.NewWriter(&encoded)
			case "zstd":
				var err error
				writer, err = zstd.NewWriter(&encoded, zstd.WithEncoderConcurrency(1))
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := writer.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			wire, display, err := Read(bytes.NewReader(encoded.Bytes()), http.Header{"Content-Encoding": {coding}}, int64(len(payload)))
			if err != nil || display != string(payload) || !bytes.Equal(wire, encoded.Bytes()) {
				t.Fatalf("codec=%s err=%v", kind, err)
			}
			_, _, err = Read(bytes.NewReader(encoded.Bytes()), http.Header{"Content-Encoding": {coding}}, int64(len(payload)-1))
			if code, _ := Code(err, "read_error"); code != "response_too_large" {
				t.Fatalf("codec=%s limit error=%v", kind, err)
			}
		})
	}
}

func TestGuardRejectsOverloadWithoutWaitingAndReleases(t *testing.T) {
	for _, limits := range []Limits{{MaxResponseBytes: -1}, {MaxResponseBytes: 1<<40 + 1}, {MaxInflight: -1}, {MaxInflight: 65537}} {
		if _, err := NewGuard(limits); err == nil {
			t.Fatalf("invalid limits accepted: %+v", limits)
		}
	}
	guard, err := NewGuard(Limits{MaxInflight: 1})
	if err != nil {
		t.Fatal(err)
	}
	release, err := guard.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = guard.Acquire(context.Background()); !errors.Is(err, ErrOverloaded) {
		t.Fatalf("overload: %v", err)
	}
	release()
	release, err = guard.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = guard.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if code, phase := Code(context.DeadlineExceeded, "request_error"); code != "timeout" || phase != "total" {
		t.Fatal("deadline classification lost")
	}
	if code, _ := Code(io.ErrUnexpectedEOF, "read_error"); code != "read_error" {
		t.Fatal("read classification lost")
	}
}
