package engine

import "testing"

func TestUserAgent(t *testing.T) {
	old := UserAgentVersion
	defer func() { UserAgentVersion = old }()

	UserAgentVersion = "dev"
	if got, want := UserAgent(), "mst/dev (+https://github.com/openmtx/meta-search-tool)"; got != want {
		t.Errorf("UserAgent() = %q, want %q", got, want)
	}

	SetUserAgentVersion("v0.5.0")
	if got, want := UserAgent(), "mst/0.5.0 (+https://github.com/openmtx/meta-search-tool)"; got != want {
		t.Errorf("UserAgent() after tag = %q, want %q", got, want)
	}

	SetUserAgentVersion("")
	if got := UserAgentVersion; got != "0.5.0" {
		t.Errorf("empty SetUserAgentVersion should leave the version unchanged, got %q", got)
	}
}
