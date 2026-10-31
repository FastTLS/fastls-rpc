// Package h3config owns explicit H3 configuration and operator-owned trust.
package h3config

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"

	fastls "github.com/FastTLS/fastls"
)

// LoadRoots loads a private, immutable startup snapshot. A nonempty file
// replaces system roots for H3 only; request DTOs cannot select this input.
func LoadRoots(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open H3 CA file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("H3 CA file must be regular")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("H3 CA file unreadable or exceeds 1 MiB")
	}
	roots := x509.NewCertPool()
	count := 0
	for len(strings.TrimSpace(string(data))) > 0 {
		// pem.Decode otherwise tolerates arbitrary leading garbage.
		data = []byte(strings.TrimSpace(string(data)))
		if !strings.HasPrefix(string(data), "-----BEGIN CERTIFICATE-----") {
			return nil, errors.New("H3 CA file must contain PEM certificates only")
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("invalid H3 CA certificate")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid H3 CA certificate")
		}
		roots.AddCert(cert)
		count++
		data = rest
	}
	if count == 0 {
		return nil, errors.New("H3 CA file contains no certificates")
	}
	return roots, nil
}

// Controls includes proxy/fingerprint metadata even when a lower priority
// setting would be hidden by another input. H3 never silently ignores it.
func Controls(headers http.Header) bool {
	for key := range headers {
		if strings.HasPrefix(strings.ToLower(key), "x-mitm-") {
			return true
		}
	}
	return false
}

func Apply(options *fastls.Options, version, rawURL string, incompatible bool, roots *x509.CertPool) error {
	if version == "" {
		return nil
	}
	if version != "h3" {
		return errors.New("http_version must be empty or h3")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return errors.New("explicit H3 requires an HTTPS destination")
	}
	headerControls := false
	for key := range options.Headers {
		if strings.HasPrefix(strings.ToLower(key), "x-mitm-") {
			headerControls = true
		}
	}
	if incompatible || options.Proxy != "" || len(options.Trailers) != 0 || headerControls {
		return errors.New("explicit H3 does not support browser/fingerprint, H2 settings, proxy controls or request trailers")
	}
	if err := enableVersion(options); err != nil {
		return err
	}
	options.VerifyTLS = true
	options.TLSRootCAs = roots
	return nil
}

// Preserve compilation with historical backends so their existing startup
// policy refusal stays testable. A missing optional H3 field always rejects;
// this bridge must never become an implicit protocol fallback.
func enableVersion(options any) error {
	value := reflect.ValueOf(options)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return errors.New("H3 requires a protocol-capable fastls core")
	}
	field := value.Elem().FieldByName("HTTPVersion")
	if !field.IsValid() || field.Kind() != reflect.String || !field.CanSet() {
		return errors.New("H3 requires a protocol-capable fastls core")
	}
	field.SetString("h3")
	return nil
}
