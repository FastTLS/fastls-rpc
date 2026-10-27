package ingress

import (
	"errors"
	"net/http"
	"time"
)

func NewHTTPServer(handler http.Handler, config HTTPConfig) (*http.Server, error) {
	if handler == nil {
		return nil, errors.New("nil ingress HTTP handler")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	gate, err := NewGate(config.MaxAdmittedRequests)
	if err != nil {
		return nil, err
	}
	timeout := config.ReadHeaderTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			release, err := gate.Acquire(r.Context())
			if err != nil {
				// Stop net/http's automatic body draining from waiting on an untrusted
				// caller after rejecting before parsing. The write side remains usable.
				r.Close = true
				w.Header().Set("Connection", "close")
				_ = http.NewResponseController(w).SetReadDeadline(time.Now())
				if config.OverloadHandler != nil {
					config.OverloadHandler.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("{\"ok\":false,\"error_code\":\"overloaded\",\"error\":\"inbound request limit reached\"}\n"))
				return
			}
			defer release()
			handler.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: timeout, ReadTimeout: config.ReadTimeout,
		IdleTimeout: config.IdleTimeout, MaxHeaderBytes: config.MaxHeaderBytes,
	}, nil
}
