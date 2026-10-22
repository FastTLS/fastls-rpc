package main

import (
	"strconv"
	"strings"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls/imitate"
)

type RequestFingerprintConfig struct {
	Browser             string
	JA3                 string
	JA4R                string
	HTTP2SettingsString string
	Override            bool
}

// applyImitateConfig 应用指纹配置到 options
func applyImitateConfig(options *fastls.Options, requestConfig *RequestFingerprintConfig) {
	if requestConfig != nil && requestConfig.Override {
		// 优先级: JA3 > JA4R > Browser
		if requestConfig.JA3 != "" {
			options.Fingerprint = fastls.Ja3Fingerprint{
				FingerprintValue: requestConfig.JA3,
			}
			return
		}

		if requestConfig.JA4R != "" {
			options.Fingerprint = fastls.Ja4Fingerprint{
				FingerprintValue: requestConfig.JA4R,
			}
			return
		}

		if requestConfig.Browser != "" {
			applyBrowserImitate(options, requestConfig.Browser)
			return
		}
	}
}

func finishBrowserUA(options *fastls.Options) {
	if options.UserAgent != "" {
		if options.Headers == nil {
			options.Headers = make(map[string]string)
		}
		options.Headers["User-Agent"] = options.UserAgent
	}
}

// applyBrowserImitate 按浏览器类型应用 imitate 指纹
func applyBrowserImitate(options *fastls.Options, browserType string) {
	browserType = strings.ToLower(strings.TrimSpace(browserType))

	// firefox151–firefox158
	if strings.HasPrefix(browserType, "firefox") && browserType != "firefox" {
		if verStr := strings.TrimPrefix(browserType, "firefox"); verStr != "" {
			if major, err := strconv.Atoi(verStr); err == nil && major >= 151 && major <= 158 {
				imitate.FirefoxVersion(options, major)
				finishBrowserUA(options)
				return
			}
		}
	}

	// chrome150–chrome160
	if strings.HasPrefix(browserType, "chrome") && browserType != "chrome" && browserType != "chromium" {
		if verStr := strings.TrimPrefix(browserType, "chrome"); verStr != "" {
			if major, err := strconv.Atoi(verStr); err == nil {
				switch major {
				case 120:
					imitate.Chrome120(options)
					finishBrowserUA(options)
					return
				case 142:
					imitate.Chrome142(options)
					finishBrowserUA(options)
					return
				}
				if major >= imitate.ChromiumVersionMin && major <= imitate.ChromiumVersionMax {
					imitate.ChromeVersion(options, major)
					finishBrowserUA(options)
					return
				}
			}
		}
	}

	// edge150–edge160
	if strings.HasPrefix(browserType, "edge") && browserType != "edge" {
		if verStr := strings.TrimPrefix(browserType, "edge"); verStr != "" {
			if major, err := strconv.Atoi(verStr); err == nil &&
				major >= imitate.ChromiumVersionMin && major <= imitate.ChromiumVersionMax {
				imitate.EdgeVersion(options, major)
				finishBrowserUA(options)
				return
			}
		}
	}

	switch browserType {
	case "chrome":
		imitate.Chrome(options)
	case "chrome120":
		imitate.Chrome120(options)
	case "chrome142":
		imitate.Chrome142(options)
	case "chromium":
		imitate.Chromium(options)
	case "edge":
		imitate.Edge(options)
	case "firefox":
		imitate.Firefox158(options)
	case "firefox144":
		imitate.Firefox144(options)
	case "firefox147":
		imitate.Firefox147(options)
	case "firefox148":
		imitate.Firefox148(options)
	case "safari":
		imitate.Safari(options)
	case "opera":
		imitate.Opera(options)
	default:
		imitate.Firefox158(options)
	}
	finishBrowserUA(options)
}
