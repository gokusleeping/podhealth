package middleware

import (
	"net"
	"net/http"
	"time"
)

type propagationRoundTripper struct {
	base http.RoundTripper
}

func (p propagationRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if rid := RequestIDFromContext(r.Context()); rid != "" && r.Header.Get(HeaderRequestID) == "" {
		r.Header.Set(HeaderRequestID, rid)
	}
	if tid := TraceIDFromContext(r.Context()); tid != "" && r.Header.Get(HeaderTraceID) == "" {
		r.Header.Set(HeaderTraceID, tid)
	}
	return p.base.RoundTrip(r)
}

func NewPropagatingClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   2 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: propagationRoundTripper{base: transport},
	}
}
