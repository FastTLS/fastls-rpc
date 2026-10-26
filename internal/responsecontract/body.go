// Package responsecontract preserves the upstream wire view and bounds decoding.
package responsecontract

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	fastls "github.com/FastTLS/fastls"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

type LimitError struct{}

func (*LimitError) Error() string { return "response exceeds configured byte limit" }

type DecodeError struct{ Err error }

func (e *DecodeError) Error() string { return "response decoding failed: " + e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

func readAll(r io.Reader, maximum int64) ([]byte, error) {
	if maximum <= 0 {
		return io.ReadAll(r)
	}
	data, err := io.ReadAll(io.LimitReader(r, maximum+1))
	if int64(len(data)) > maximum {
		return nil, &LimitError{}
	}
	return data, err
}

// Read returns the exact encoded body alongside the legacy display string.
// The optional limit applies independently to the wire body and every decoding
// layer, so compressed input cannot bypass it.
func Read(body io.Reader, headers http.Header, maximum int64) (wire []byte, display string, err error) {
	wire, err = readAll(body, maximum)
	if err != nil {
		return nil, "", err
	}
	data := wire
	var codings []string
	for _, value := range headers.Values("Content-Encoding") {
		for _, part := range strings.Split(value, ",") {
			if coding := strings.ToLower(strings.TrimSpace(part)); coding != "" && coding != "identity" {
				codings = append(codings, coding)
			}
		}
	}
	if len(codings) > 4 {
		return nil, "", &DecodeError{errors.New("more than four encoding layers")}
	}
	for i := len(codings) - 1; i >= 0; i-- {
		var reader io.Reader
		var close func()
		switch codings[i] {
		case "gzip", "x-gzip":
			r, e := gzip.NewReader(bytes.NewReader(data))
			if e != nil {
				return nil, "", &DecodeError{e}
			}
			reader = r
			close = func() { r.Close() }
		case "deflate":
			r, e := zlib.NewReader(bytes.NewReader(data))
			if e != nil {
				r = flate.NewReader(bytes.NewReader(data))
			}
			reader = r
			close = func() { r.Close() }
		case "br":
			reader = brotli.NewReader(bytes.NewReader(data))
		case "zstd":
			// Bound the decoder's own allocation as well as the resulting bytes.
			options := []zstd.DOption{zstd.WithDecoderConcurrency(1)}
			if maximum > 0 {
				memory := uint64(maximum)
				if memory < 64<<20 {
					memory = 64 << 20
				}
				options = append(options, zstd.WithDecoderMaxMemory(memory))
			}
			r, e := zstd.NewReader(bytes.NewReader(data), options...)
			if e != nil {
				return nil, "", &DecodeError{e}
			}
			reader = r
			close = r.Close
		default:
			return nil, "", &DecodeError{fmt.Errorf("unsupported content encoding %q", codings[i])}
		}
		data, err = readAll(reader, maximum)
		if close != nil {
			close()
		}
		if err != nil {
			var limit *LimitError
			if errors.As(err, &limit) {
				return nil, "", err
			}
			return nil, "", &DecodeError{err}
		}
	}
	if len(codings) > 0 {
		return wire, strings.ToValidUTF8(string(data), "\uFFFD"), nil
	}
	return wire, strings.ToValidUTF8(fastls.DecompressBody(data, nil, []string{headers.Get("Content-Type")}), "\uFFFD"), nil
}

func Base64(wire []byte) string { return base64.StdEncoding.EncodeToString(wire) }

// Code preserves cancellation and timeout semantics without parsing messages.
func Code(err error, fallback string) (string, string) {
	var denied interface{ AddressPolicyReason() string }
	var phase *fastls.TimeoutError
	var limit *LimitError
	var decoding *DecodeError
	switch {
	case errors.As(err, &phase):
		return "timeout", phase.Phase
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", "total"
	case errors.Is(err, context.Canceled):
		return "cancelled", ""
	case errors.As(err, &limit):
		return "response_too_large", ""
	case errors.As(err, &decoding):
		return "decode_error", ""
	case errors.As(err, &denied):
		return "address_denied", ""
	default:
		return fallback, ""
	}
}

// ErrorMessage avoids leaking URL/userinfo/query through outer url.Error
// wrappers for policy rejections. Other errors retain the legacy message.
func ErrorMessage(err error) string {
	var denied interface{ AddressPolicyReason() string }
	if errors.As(err, &denied) {
		return "address policy denied request: " + denied.AddressPolicyReason()
	}
	return err.Error()
}
