// Package httpx carries the per-run HTTP client on the request context (§7).
//
// Every engine request goes through Client(ctx), so one run's transport — for
// example a per-search outbound proxy (§6) — applies to every engine in that
// run without global state. Runs that attach different clients are isolated:
// concurrent searches with different proxies never share a transport.
package httpx

import (
	"context"
	"net/http"
)

// ctxKey is unexported so only this package can attach a client.
type ctxKey struct{}

// WithClient returns a context that makes client the transport for all HTTP
// calls made under it. A nil client is ignored rather than attached.
func WithClient(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, client)
}

// Client returns the client attached by WithClient, falling back to
// http.DefaultClient (which honors HTTP_PROXY/HTTPS_PROXY via the default
// transport, §6).
func Client(ctx context.Context) *http.Client {
	if ctx != nil {
		if c, ok := ctx.Value(ctxKey{}).(*http.Client); ok && c != nil {
			return c
		}
	}
	return http.DefaultClient
}
