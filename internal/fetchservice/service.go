package fetchservice

import (
	"context"
	"fmt"
	"maps"
	"net/http"

	fastls "github.com/FastTLS/fastls"
	profileprovider "github.com/FastTLS/fastls-rpc/internal/fingerprint"
	"github.com/FastTLS/fastls-rpc/internal/responsecontract"
)

const defaultTimeoutSeconds = 30

// Params is the transport-independent request contract shared by JSON-RPC and gRPC.
type Params struct {
	IncludeBodyBase64 bool
	// MaxResponseBytes is set by the service operator, never by a wire DTO.
	MaxResponseBytes    int64
	URL                 string
	Method              string
	Headers             map[string]string
	Trailers            http.Header
	Body                string
	Proxy               string
	ParentProxy         string
	Timeout             int
	DisableRedirect     bool
	UserAgent           string
	FingerprintSet      bool
	FingerprintType     string
	FingerprintValue    string
	Browser             string
	Cookies             []fastls.Cookie
	HTTP2SettingsString string
}

// Result is the transport-independent fetch response.
type Result struct {
	OK           bool
	Status       int
	Protocol     string
	FinalURL     string
	Headers      map[string]string
	Trailers     http.Header
	Body         string
	Error        string
	HeaderValues http.Header
	BodyBase64   string
	ErrorCode    string
	TimeoutPhase string
}

// ConfigError identifies request configuration failures that callers should
// expose as invalid-argument errors rather than upstream request failures.
type ConfigError struct {
	err error
}

func (e *ConfigError) Error() string { return e.err.Error() }
func (e *ConfigError) Unwrap() error { return e.err }

// BuildOptions applies the common request defaults and precedence rules.
// parent_proxy wins over proxy; an explicit User-Agent wins over a generated
// browser profile; and an explicit HTTP/2 settings string wins over a profile.
func BuildOptions(params Params) (fastls.Options, error) {
	if params.URL == "" {
		return fastls.Options{}, &ConfigError{err: fmt.Errorf("url is required")}
	}
	if params.Timeout < 0 {
		return fastls.Options{}, &ConfigError{err: fmt.Errorf("timeout must not be negative")}
	}
	if params.Method == "" {
		params.Method = "GET"
	}
	if params.Timeout == 0 {
		params.Timeout = defaultTimeoutSeconds
	}

	options := fastls.Options{
		Headers:         maps.Clone(params.Headers),
		Body:            params.Body,
		Trailers:        params.Trailers.Clone(),
		Proxy:           params.Proxy,
		Timeout:         params.Timeout,
		DisableRedirect: params.DisableRedirect,
		Cookies:         append([]fastls.Cookie(nil), params.Cookies...),
	}
	if options.Headers == nil {
		options.Headers = make(map[string]string)
	}
	if params.ParentProxy != "" {
		options.Proxy = params.ParentProxy
	}

	var ja3, ja4r string
	if params.FingerprintSet || params.FingerprintType != "" || params.FingerprintValue != "" {
		if params.FingerprintType == "" || params.FingerprintValue == "" {
			return fastls.Options{}, &ConfigError{err: fmt.Errorf("fingerprint requires both type and value")}
		}
		switch params.FingerprintType {
		case "ja3":
			ja3 = params.FingerprintValue
		case "ja4", "ja4r":
			ja4r = params.FingerprintValue
		default:
			return fastls.Options{}, &ConfigError{err: fmt.Errorf("unsupported fingerprint type %q", params.FingerprintType)}
		}
	}

	if ja3 != "" || ja4r != "" || params.Browser != "" {
		if err := profileprovider.Apply(&options, params.Browser, ja3, ja4r); err != nil {
			return fastls.Options{}, &ConfigError{err: fmt.Errorf("invalid browser fingerprint: %w", err)}
		}
	} else if err := profileprovider.ApplyBrowser(&options, "firefox"); err != nil {
		return fastls.Options{}, fmt.Errorf("default browser fingerprint unavailable: %w", err)
	}

	if params.UserAgent != "" {
		options.UserAgent = params.UserAgent
	}
	if params.HTTP2SettingsString != "" {
		if _, _, err := fastls.ParseH2SettingsStringWithPHeaderOrder(params.HTTP2SettingsString); err != nil {
			return fastls.Options{}, &ConfigError{err: fmt.Errorf("invalid http2_settings_string: %w", err)}
		}
		options.HTTP2SettingsString = params.HTTP2SettingsString
	}
	return options, nil
}

// Fetch executes one request while preserving caller cancellation through the
// downstream response body lifecycle.
func Fetch(ctx context.Context, client fastls.Fastls, params Params) (Result, error) {
	options, err := BuildOptions(params)
	if err != nil {
		return Result{}, err
	}
	method := params.Method
	if method == "" {
		method = "GET"
	}

	ctx, cancel := responsecontract.RequestContext(ctx, options.Timeout)
	defer cancel()
	response, requestErr := client.DoContext(ctx, params.URL, options, method)
	if response.Body != nil {
		defer response.Body.Close()
	}
	defer response.Client.CloseIdleConnections()
	if requestErr != nil {
		code, phase := responsecontract.Code(requestErr, "request_error")
		return Result{Status: response.Status, Protocol: response.Protocol, FinalURL: response.FinalURL, Error: responsecontract.ErrorMessage(requestErr), ErrorCode: code, TimeoutPhase: phase}, nil
	}

	wire, decodedBody, err := responsecontract.ReadContext(ctx, response.Body, http.Header(response.HeaderValues), params.MaxResponseBytes)
	if err != nil {
		code, phase := responsecontract.Code(err, "read_error")
		return Result{Status: response.Status, Protocol: response.Protocol, FinalURL: response.FinalURL, Headers: response.Headers, HeaderValues: http.Header(response.HeaderValues).Clone(), Error: "读取响应体失败: " + err.Error(), ErrorCode: code, TimeoutPhase: phase}, nil
	}

	bodyBase64 := ""
	if params.IncludeBodyBase64 {
		bodyBase64 = responsecontract.Base64(wire)
	}
	return Result{
		OK:           true,
		Status:       response.Status,
		Protocol:     response.Protocol,
		FinalURL:     response.FinalURL,
		Headers:      response.Headers,
		Trailers:     response.Trailer.Clone(),
		Body:         decodedBody,
		HeaderValues: http.Header(response.HeaderValues).Clone(),
		BodyBase64:   bodyBase64,
	}, nil
}
