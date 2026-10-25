package fingerprint

import (
	"fmt"
	"strconv"
	"strings"

	fastls "github.com/FastTLS/fastls"
	fastlsimitate "github.com/FastTLS/fastls/imitate"
	generators "github.com/FastTLS/imitate"
)

var provider = generators.New()

func Apply(options *fastls.Options, browser, ja3, ja4r string) error {
	switch {
	case ja3 != "":
		options.Fingerprint = fastls.Ja3Fingerprint{FingerprintValue: ja3}
		return nil
	case ja4r != "":
		options.Fingerprint = fastls.Ja4Fingerprint{FingerprintValue: ja4r}
		return nil
	case browser != "":
		return ApplyBrowser(options, browser)
	default:
		return nil
	}
}

func ApplyBrowser(options *fastls.Options, browser string) error {
	_, err := fastlsimitate.Use(options, Selector(browser), fastlsimitate.WithProvider(provider))
	return err
}

func Selector(browser string) string {
	browser = strings.ToLower(strings.TrimSpace(browser))
	selector := "firefox/158/windows"

	if strings.HasPrefix(browser, "firefox") && browser != "firefox" {
		if major, err := strconv.Atoi(strings.TrimPrefix(browser, "firefox")); err == nil &&
			(major >= 151 && major <= 158 || major == 144 || major == 147 || major == 148) {
			selector = fmt.Sprintf("firefox/%d/windows", major)
		}
	}
	if strings.HasPrefix(browser, "chrome") && browser != "chrome" && browser != "chromium" {
		if major, err := strconv.Atoi(strings.TrimPrefix(browser, "chrome")); err == nil &&
			(major == 120 || major == 142 || major >= 150 && major <= 160) {
			selector = fmt.Sprintf("chrome/%d/windows", major)
		}
	}
	if strings.HasPrefix(browser, "edge") && browser != "edge" {
		if major, err := strconv.Atoi(strings.TrimPrefix(browser, "edge")); err == nil && major >= 150 && major <= 160 {
			selector = fmt.Sprintf("edge/%d/windows", major)
		}
	}

	switch browser {
	case "chrome":
		selector = "chrome/160/windows"
	case "chrome120":
		selector = "chrome/120/windows"
	case "chrome142":
		selector = "chrome/142/windows"
	case "chromium":
		selector = "chromium/160/windows"
	case "edge":
		selector = "edge/160/windows"
	case "firefox":
		selector = "firefox/158/windows"
	case "firefox144":
		selector = "firefox/144/windows"
	case "firefox147":
		selector = "firefox/147/windows"
	case "firefox148":
		selector = "firefox/148/windows"
	case "safari":
		selector = "safari/18.7.3/ipados"
	case "opera":
		selector = "opera/145/windows"
	}
	return selector
}
