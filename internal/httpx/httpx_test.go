package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_FallbackToDefault(t *testing.T) {
	t.Parallel()

	if got := Client(context.Background()); got != http.DefaultClient {
		t.Fatalf("Client(background) = %p, want http.DefaultClient (%p)", got, http.DefaultClient)
	}
	// A typed-nil pointer must not be treated as an attached client.
	ctx := context.WithValue(context.Background(), ctxKey{}, (*http.Client)(nil))
	if got := Client(ctx); got != http.DefaultClient {
		t.Fatalf("Client(typed nil) = %p, want http.DefaultClient", got)
	}
}

func TestWithClient_RoundTrip(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// A client with a redirect policy that proves it is the one in use.
	custom := &http.Client{}
	ctx := WithClient(context.Background(), custom)

	if got := Client(ctx); got != custom {
		t.Fatalf("Client(ctx) = %p, want attached client %p", got, custom)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := Client(ctx).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestWithClient_NilIgnored(t *testing.T) {
	t.Parallel()

	ctx := WithClient(context.Background(), nil)
	if got := Client(ctx); got != http.DefaultClient {
		t.Fatalf("Client(after nil attach) = %p, want http.DefaultClient", got)
	}
}

func TestClient_IsolatedPerContext(t *testing.T) {
	t.Parallel()

	a := &http.Client{}
	b := &http.Client{}
	ctxA := WithClient(context.Background(), a)
	ctxB := WithClient(context.Background(), b)

	if Client(ctxA) != a || Client(ctxB) != b {
		t.Fatal("clients leaked across contexts")
	}
	if Client(context.Background()) == a || Client(context.Background()) == b {
		t.Fatal("attached client leaked into an unrelated context")
	}
}
