package h3config

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fastls "github.com/FastTLS/fastls"
)

func TestExplicitH3RejectsConflictsAndVerifiesTLS(t *testing.T) {
	for _, old := range []any{nil, &struct{}{}, &struct{ HTTPVersion int }{}} {
		if enableVersion(old) == nil {
			t.Fatal("old backend silently accepted H3")
		}
	}
	for _, version := range []string{"h2", "H3", "auto", "h4"} {
		if Apply(&fastls.Options{}, version, "https://example.test", false, nil) == nil {
			t.Fatalf("accepted %q", version)
		}
	}
	for _, tc := range []struct {
		url      string
		options  fastls.Options
		conflict bool
	}{
		{"http://example.test", fastls.Options{}, false},
		{"https://example.test", fastls.Options{}, true},
		{"https://example.test", fastls.Options{Proxy: "http://proxy.test"}, false},
		{"https://example.test", fastls.Options{Trailers: http.Header{"X-End": {"x"}}}, false},
		{"https://example.test", fastls.Options{Headers: map[string]string{"x-MiTm-Proxy": ""}}, false},
	} {
		if Apply(&tc.options, "h3", tc.url, tc.conflict, nil) == nil {
			t.Fatal("accepted incompatible request")
		}
	}
	options := fastls.Options{UserAgent: "plain-header"}
	if err := Apply(&options, "h3", "https://example.test", false, nil); err != nil {
		t.Fatal(err)
	}
	if options.HTTPVersion != fastls.HTTPVersion3 || !options.VerifyTLS || options.Profile != nil || options.Fingerprint != nil {
		t.Fatal("H3 inherited fingerprint or disabled verification")
	}
	if err := Apply(&options, "", "http://example.test", true, nil); err != nil {
		t.Fatal("default behavior changed")
	}
}

func TestRootsAreBoundedStrictAndSnapshotOwned(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	path := filepath.Join(t.TempDir(), "roots.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	roots, err := LoadRoots(path)
	if err != nil {
		t.Fatal(err)
	}
	before := len(roots.Subjects())
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if len(roots.Subjects()) != before || before != 1 {
		t.Fatal("startup trust changed")
	}
	for _, bad := range [][]byte{nil, []byte("garbage"), append([]byte("prefix"), data...), append(data, []byte("junk")...), []byte(strings.Repeat("x", (1<<20)+1))} {
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRoots(path); err == nil {
			t.Fatal("invalid trust file accepted")
		}
	}
	if _, err := LoadRoots(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
}
