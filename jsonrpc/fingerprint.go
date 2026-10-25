package main

import (
	fastls "github.com/FastTLS/fastls"
	profileprovider "github.com/FastTLS/fastls-rpc/internal/fingerprint"
)

type RequestFingerprintConfig struct {
	Browser             string
	JA3                 string
	JA4R                string
	HTTP2SettingsString string
	Override            bool
}

func applyImitateConfig(options *fastls.Options, requestConfig *RequestFingerprintConfig) error {
	if requestConfig == nil || !requestConfig.Override {
		return nil
	}
	return profileprovider.Apply(options, requestConfig.Browser, requestConfig.JA3, requestConfig.JA4R)
}

func applyBrowserImitate(options *fastls.Options, browserType string) error {
	return profileprovider.ApplyBrowser(options, browserType)
}
