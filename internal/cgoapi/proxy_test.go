package cgoapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/httpx"
)

func TestContextWithProxy_EmptyKeepsDefault(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "   ", "\t"} {
		ctx, cleanup, err := ContextWithProxy(context.Background(), in)
		if err != nil {
			t.Fatalf("ContextWithProxy(%q) error = %v", in, err)
		}
		if cleanup == nil {
			t.Fatalf("ContextWithProxy(%q) returned a nil cleanup", in)
		}
		if got := httpx.Client(ctx); got != http.DefaultClient {
			t.Fatalf("ContextWithProxy(%q) attached a client, want the default", in)
		}
		cleanup()
	}
}

func TestContextWithProxy_InvalidIsUsageErrorAndRedacted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"no scheme", "host:8080"},
		{"missing host", "http://"},
		{"unparseable", "http://user:supersecret@"},
		{"unsupported scheme", "ftp://user:supersecret@host:21"},
		{"socks4 unsupported", "socks4://user:supersecret@host:1080"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := ContextWithProxy(context.Background(), tt.in)
			if err == nil {
				t.Fatalf("ContextWithProxy(%q) = nil error, want usage error", tt.in)
			}
			if strings.Contains(err.Error(), "supersecret") || strings.Contains(err.Error(), "user:") {
				t.Fatalf("error leaks credentials: %v", err)
			}
		})
	}
}

// TestContextWithProxy_HTTPProxyTraversal proves an engine request traverses
// the configured proxy: the proxy sees the absolute request URL.
func TestContextWithProxy_HTTPProxyTraversal(t *testing.T) {
	t.Parallel()

	var gotHost, gotPath atomic.Value
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.IsAbs() {
			gotHost.Store(r.URL.Host)
			gotPath.Store(r.URL.Path)
		}
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxySrv.Close()

	ctx, cleanup, err := ContextWithProxy(context.Background(), proxySrv.URL)
	if err != nil {
		t.Fatalf("ContextWithProxy: %v", err)
	}
	defer cleanup()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/resource", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := httpx.Client(ctx).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "proxied" {
		t.Fatalf("body = %q, want %q", body, "proxied")
	}
	if gotHost.Load() != "example.invalid" {
		t.Fatalf("proxy saw host %v, want example.invalid", gotHost.Load())
	}
	if gotPath.Load() != "/resource" {
		t.Fatalf("proxy saw path %v, want /resource", gotPath.Load())
	}
}

// TestContextWithProxy_ConnectTunnel proves HTTPS traffic tunnels through an
// HTTP proxy via CONNECT, with TLS verification left on (a custom root is only
// installed to trust the in-process test certificate).
func TestContextWithProxy_ConnectTunnel(t *testing.T) {
	t.Parallel()

	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secure"))
	}))
	defer target.Close()

	var connected atomic.Bool
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "expected CONNECT", http.StatusMethodNotAllowed)
			return
		}
		dst, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer dst.Close()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		connected.Store(true)
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(dst, conn)
			close(done)
		}()
		_, _ = io.Copy(conn, dst)
		<-done
	}))
	defer proxySrv.Close()

	ctx, cleanup, err := ContextWithProxy(context.Background(), proxySrv.URL)
	if err != nil {
		t.Fatalf("ContextWithProxy: %v", err)
	}
	defer cleanup()

	client := httpx.Client(ctx)
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	pool := x509.NewCertPool()
	pool.AddCert(target.Certificate())
	tr.TLSClientConfig = &tls.Config{RootCAs: pool}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secure" {
		t.Fatalf("body = %q, want %q", body, "secure")
	}
	if !connected.Load() {
		t.Fatal("proxy never received a CONNECT")
	}
}

// TestContextWithProxy_IsolatedConcurrent proves two concurrent runs with
// different proxies keep their own client and do not leak across contexts.
func TestContextWithProxy_IsolatedConcurrent(t *testing.T) {
	t.Parallel()

	newProxy := func(tag string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(20 * time.Millisecond) // widen the overlap window
			_, _ = w.Write([]byte(tag))
		}))
	}
	proxyA := newProxy("A")
	defer proxyA.Close()
	proxyB := newProxy("B")
	defer proxyB.Close()

	fetch := func(proxyURL string) (string, error) {
		ctx, cleanup, err := ContextWithProxy(context.Background(), proxyURL)
		if err != nil {
			return "", err
		}
		defer cleanup()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/", nil)
		if err != nil {
			return "", err
		}
		resp, err := httpx.Client(ctx).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil
	}

	type result struct {
		tag string
		err error
	}
	results := make(chan result, 2)
	go func() {
		tag, err := fetch(proxyA.URL)
		results <- result{tag, err}
	}()
	go func() {
		tag, err := fetch(proxyB.URL)
		results <- result{tag, err}
	}()

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("fetch: %v", r.err)
		}
		seen[r.tag] = true
	}
	if !seen["A"] || !seen["B"] {
		t.Fatalf("proxies crossed: seen = %v, want A and B", seen)
	}
}

// TestContextWithProxy_CleanupClosesIdleConnections proves cleanup discards
// pooled connections instead of leaving them open past the run.
func TestContextWithProxy_CleanupClosesIdleConnections(t *testing.T) {
	t.Parallel()

	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2")
		_, _ = w.Write([]byte("ok"))
	}))
	defer proxySrv.Close()

	ctx, cleanup, err := ContextWithProxy(context.Background(), proxySrv.URL)
	if err != nil {
		t.Fatalf("ContextWithProxy: %v", err)
	}

	roundTrip := func() bool {
		var reused bool
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
		req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, "http://example.invalid/x", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		resp, err := httpx.Client(ctx).Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return reused
	}

	if roundTrip() {
		t.Fatal("first request unexpectedly reused a connection")
	}
	if !roundTrip() {
		t.Fatal("second request did not reuse the pooled connection")
	}
	cleanup()
	if roundTrip() {
		t.Fatal("request after cleanup reused a discarded connection")
	}
}

// TestContextWithProxy_SOCKS5DoesNotFallBackToDirect proves a SOCKS5 proxy is
// used for the dial and that an unreachable one fails instead of connecting
// directly.
func TestContextWithProxy_SOCKS5DoesNotFallBackToDirect(t *testing.T) {
	t.Parallel()

	ctx, cleanup, err := ContextWithProxy(context.Background(), "socks5h://127.0.0.1:1")
	if err != nil {
		t.Fatalf("ContextWithProxy: %v", err)
	}
	defer cleanup()

	client := httpx.Client(ctx)
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("SOCKS5 transport still has an environment proxy resolver")
	}
	if tr.DialContext == nil {
		t.Fatal("SOCKS5 transport has no proxy dialer")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("request through an unreachable SOCKS5 proxy unexpectedly succeeded")
	}
}
