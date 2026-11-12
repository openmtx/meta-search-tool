package engine

import "strings"

// ProjectURL is the contactable project URL embedded in the API-engine
// User-Agent (§12). Keeping it in one place means every structured API engine
// identifies both the tool and a way to contact its maintainers.
const ProjectURL = "https://github.com/openmtx/meta-search-tool"

// UserAgentVersion is the version reported in the API-engine User-Agent. It is
// set once at startup from the build version (see cmd/mst and cgo); "dev" is the
// fallback for local builds and tests.
var UserAgentVersion = "dev"

// SetUserAgentVersion sets the version embedded in UserAgent. The leading "v"
// of a release tag is stripped so the UA reads "mst/0.5.0", not "mst/v0.5.0".
// An empty value leaves the "dev" fallback in place.
func SetUserAgentVersion(v string) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if v == "" {
		return
	}
	UserAgentVersion = v
}

// UserAgent returns the contactable User-Agent used by structured API engines
// (§12): "mst/<version> (+<project URL>)". Scrape engines keep their browser UA
// carve-out and do not call this.
func UserAgent() string {
	return "mst/" + UserAgentVersion + " (+" + ProjectURL + ")"
}
