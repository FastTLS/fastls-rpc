package fetchservice

import "testing"

func TestH3OptionsDoNotApplyDefaultFingerprint(t *testing.T) {
	options, err := BuildOptions(Params{URL: "https://example.test", HTTPVersion: "h3", UserAgent: "custom"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Profile != nil || options.Fingerprint != nil || options.HTTP2SettingsString != "" || !options.VerifyTLS || options.UserAgent != "custom" {
		t.Fatal("H3 inherited TCP identity")
	}
	for _, params := range []Params{
		{HTTPVersion: "h4"}, {HTTPVersion: "h3", Browser: "firefox"}, {HTTPVersion: "h3", FingerprintSet: true},
		{HTTPVersion: "h3", FingerprintValue: "q-selector"}, {HTTPVersion: "h3", HTTP2SettingsString: "x"},
		{HTTPVersion: "h3", Proxy: "http://proxy.test"}, {HTTPVersion: "h3", ParentProxy: "http://proxy.test"},
	} {
		params.URL = "https://example.test"
		if _, err := BuildOptions(params); err == nil {
			t.Fatal("unsupported combination accepted")
		}
	}
}
