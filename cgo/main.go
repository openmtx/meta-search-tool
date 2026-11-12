// CGO bridge entry point for mst.
// Builds as -buildmode=c-shared to produce _mst.so for Python bindings.
package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	"os"
	"runtime/debug"
	"strings"
	"time"
	"unsafe"

	"github.com/openmtx/meta-search-tool/internal/cgoapi"
	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/format"
)

// All engines are registered by cgoapi.DefaultRegistry() (called inside
// Search/ListEngines); no engine package has an init() side effect, so no
// blank imports are needed here.

// main is required by -buildmode=c-shared. It seeds the API-engine User-Agent
// version from the build environment (MST_VERSION) or Go build info so the
// Python binding identifies itself like the CLI.
func main() {
	if v := os.Getenv("MST_VERSION"); v != "" {
		engine.SetUserAgentVersion(v)
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		engine.SetUserAgentVersion(info.Main.Version)
	}
}

// MSTSearch executes a meta-search query via the mst run() pipeline.
// Returns JSON string on success, or structured error on failure.
// The caller must free the returned pointer with MSTFreeResult.
//
// Parameters:
//   - query: search query (UTF-8)
//   - engines_csv: comma-separated engine whitelist, "" = defaults
//   - limit: max merged results (1–100)
//   - offset: skip first N results
//   - rrf_k: RRF smoothing constant (use 60 for default)
//   - timeout_str: duration string, e.g. "8s"
//   - weights_json: JSON object of per-engine weights, "{}" = none
//   - proxy: per-run proxy URL (http/https/socks5/socks5h), "" = environment
//
// Appending proxy is a v0.6 ABI change: the Python wheel always ships the
// matching _mst.so, but standalone C callers must rebuild against the new header.
//
//export MSTSearch
func MSTSearch(query *C.char, enginesCSV *C.char, limit, offset, rrfK int, timeoutStr *C.char, weightsJSON *C.char, proxy *C.char) *C.char {
	q := C.GoString(query)
	if strings.TrimSpace(q) == "" {
		return cstr(`{"error":{"code":"usage","message":"empty query","exit":2}}`)
	}

	var engineList []string
	if engStr := C.GoString(enginesCSV); engStr != "" {
		for _, e := range strings.Split(engStr, ",") {
			e = strings.TrimSpace(e)
			if e != "" {
				engineList = append(engineList, e)
			}
		}
	}

	toStr := C.GoString(timeoutStr)
	globalTo, err := time.ParseDuration(toStr)
	if err != nil || globalTo <= 0 {
		return cstr(`{"error":{"code":"usage","message":"invalid timeout","exit":2}}`)
	}

	weights := make(map[string]float64)
	if wJSON := C.GoString(weightsJSON); wJSON != "" && wJSON != "{}" {
		if err := json.Unmarshal([]byte(wJSON), &weights); err != nil {
			return cstr(`{"error":{"code":"usage","message":"invalid weights JSON","exit":2}}`)
		}
	}

	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	opts := cgoapi.SearchOptions{
		Engines: engineList,
		Limit:   limit,
		Offset:  offset,
		RRFK:    rrfK,
		Timeout: globalTo,
		Weights: weights,
		Proxy:   C.GoString(proxy),
	}

	result, err := cgoapi.Search(q, opts)
	if err != nil {
		return cstr(string(format.MarshalError(format.ErrCodeUsage, err.Error(), 2)))
	}

	// Marshal envelope to JSON
	data, err := json.Marshal(result.Envelope)
	if err != nil {
		return cstr(string(format.MarshalError(format.ErrCodeUsage, err.Error(), 2)))
	}

	if result.ExitCode != 0 {
		msg := "no results"
		if len(result.Envelope.Engines) == 0 {
			msg = "no results: no ready engines (try --list-engines)"
		} else if result.ExitCode == 3 {
			msg = "engine failure"
		}
		code := format.ErrCodeNoResults
		if result.ExitCode == 3 {
			code = format.ErrCodeEngineFailure
		}
		return cstr(string(format.MarshalError(code, msg, result.ExitCode)))
	}

	return cstr(string(data) + "\n")
}

// MSTListEngines lists all registered engines with their operational state.
// The caller must free the returned pointer with MSTFreeResult.
//
//export MSTListEngines
func MSTListEngines() *C.char {
	result, err := cgoapi.ListEngines()
	if err != nil {
		return cstr(`[]`)
	}

	data, err := json.Marshal(result.Entries)
	if err != nil {
		return cstr(`[]`)
	}

	return cstr(string(data) + "\n")
}

// MSTFreeResult frees a CString returned by MSTSearch or MSTListEngines.
//
//export MSTFreeResult
func MSTFreeResult(ptr *C.char) {
	C.free(unsafe.Pointer(ptr))
}

// --- helpers ---

func cstr(s string) *C.char {
	return C.CString(s)
}
