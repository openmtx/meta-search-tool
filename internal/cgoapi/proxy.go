package cgoapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/proxy"

	"github.com/openmtx/meta-search-tool/internal/httpx"
)

// ContextWithProxy attaches a proxy-aware HTTP client to ctx (§6).
//
// rawProxy is the per-run proxy URL: http, https, socks5, or socks5h, with
// optional credentials. An empty (or whitespace-only) value returns ctx
// unchanged, so the standard HTTP_PROXY/HTTPS_PROXY environment behavior
// applies. The returned cleanup closes idle connections and is always non-nil.
//
// An explicit proxy deliberately bypasses NO_PROXY (DECISIONS 2026-11-12): the
// caller asked for a specific egress path, so it is never silently skipped.
// Invalid URLs and unsupported schemes are usage errors; the raw input is
// never included in the error text, so credentials cannot leak.
func ContextWithProxy(ctx context.Context, rawProxy string) (context.Context, func(), error) {
	noop := func() {}
	if strings.TrimSpace(rawProxy) == "" {
		return ctx, noop, nil
	}

	u, err := url.Parse(rawProxy)
	if err != nil || u.Host == "" {
		return ctx, noop, fmt.Errorf("invalid proxy URL (expected scheme://host:port)")
	}

	// Clone the default transport to inherit its timeouts and dialer, but never
	// mutate http.DefaultTransport itself (§12: no global state).
	var tr *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = base.Clone()
	} else {
		tr = &http.Transport{}
	}

	switch u.Scheme {
	case "http", "https":
		tr.Proxy = http.ProxyURL(u)
	case "socks5", "socks5h":
		// Both spellings resolve names at the proxy (remote DNS): x/net's SOCKS5
		// dialer forwards hostnames instead of resolving them locally.
		var auth *proxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: password}
		}
		dialer, derr := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if derr != nil {
			return ctx, noop, fmt.Errorf("invalid SOCKS5 proxy configuration")
		}
		cd, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return ctx, noop, fmt.Errorf("SOCKS5 proxy does not support context cancellation")
		}
		tr.Proxy = nil // explicit proxy only — never fall back to env or direct
		tr.DialContext = cd.DialContext
	default:
		return ctx, noop, fmt.Errorf("unsupported proxy scheme %q (use http, https, socks5, or socks5h)", u.Scheme)
	}

	client := &http.Client{Transport: tr}
	return httpx.WithClient(ctx, client), func() { tr.CloseIdleConnections() }, nil
}
