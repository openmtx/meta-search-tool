# mst-python — Python / CGO Extension

**Version:** 0.5.0

**Status:** Active
**Module:** `mst` (PyPI: `mst`)

> Note: PyPI name is `mst` but the import package is also `mst`. No naming conflict —
> the existing `mst` on PyPI is a different package (version 0.0.1, unused).  
**License:** Apache-2.0 (same as mst)

## Overview

`mst-python` is a thin Python extension that exposes the full mst search engine
via CGO bindings. It wraps the existing Go codebase (`internal/engine`,
`internal/search`, `internal/rank`) through a C API bridge, giving Python code
direct access to concurrent multi-engine search with RRF ranking — zero subprocess
overhead, zero external binary dependency.

## Goals

- **Zero subprocess overhead** — CGO calls into the Go runtime directly; no
  `subprocess.Popen`, no JSON marshalling across process boundaries.
- **No prebuilt binary** — the Go package compiles as part of the Python wheel
  build (`go build -buildmode=c-shared`). Users install via `pip install mst-python`
  and get a single `.so` file with everything baked in.
- **Full feature parity** — every engine, flag, and output shape from the Go
  codebase is accessible through the Python API.
- **Async-compatible** — the CGO bridge releases the GIL during search, so
  Python `asyncio` loops are not blocked.

## pyproject.toml Template

```toml
[build-system]
requires = ["setuptools>=70.0", "cython>=3.0", "wheel"]
build-backend = "setuptools.build_meta"

[project]
name = "mst-python"
version = "0.1.0"
description = "CGO Python bindings for mst (Meta-Search Tool)"
requires-python = ">=3.11"
license = {text = "Apache-2.0"}
authors = [{name = "mst contributors"}]
classifiers = [
    "Programming Language :: Python :: 3.11",
    "Programming Language :: Python :: 3.12",
    "Programming Language :: Python :: 3.13",
    "License :: OSI Approved :: Apache Software License",
]
dependencies = []

[project.optional-dependencies]
mst = ["mst-python>=0.1,<1.0"]  # for nanobot

[tool.setuptools.packages.find]
where = ["src"]

[tool.cython]
# Cython compile options
annotate = false
```

## Architecture

```
┌──────────────────────────────────────────────┐
│  Python (nanobot / any consumer)             │
│                                              │
│  import mst                                  │
│  result = mst.search("query")                |
└──────────────────┬───────────────────────────┘
                   │ CGO (c-shared library)
┌──────────────────▼───────────────────────────┐
│  cgo/mst_cgo.go  ← C API bridge             │
│                                              │
│  MSTSearch(query, enginesCSV, limit, ...)    │
│    → parses args → calls run()               │
│    → marshals result → C.CString(JSON)       │
│                                              │
│  MSTListEngines()                            │
│    → resolves plan → returns engine listing  │
└──────────────────┬───────────────────────────┘
                   │ Go runtime (linked in .so)
┌──────────────────▼───────────────────────────┐
│  internal/engine/     ← engine implementations│
│  internal/search/     ← concurrent dispatch   │
│  internal/rank/       ← RRF ranking           │
│  internal/format/     ← JSON envelope         │
│  internal/config/     ← config loading        │
└──────────────────────────────────────────────┘
```

### Build Flow

```
pip install mst-python
  → setuptools/cython builds the extension
    → CGO runs `go build -buildmode=c-shared -o _mst.so` (from cgo/)
      → embeds Go runtime + all engine packages into shared library
    → Cython compiles _mst.pyx → _mst.cpython-*.so (links against _mst.so)
  → wheel contains: _mst.so (CGO bridge), _mst.cpython-*.so (Python binding)
```

The CGO shared library (`_mst.so`) is a **relative import** at runtime — the
Python binding loads it from the same package directory using `ctypes.CDLL` with
an absolute path resolved via `importlib.resources`.

## C API (exported from `cgo/mst_cgo.go`)

All functions return `*C.char` containing a JSON string, or `NULL` on error.
The caller owns the returned memory and must call `free()`.

### `MSTSearch`

```c
char* MSTSearch(
    const char* query,           // search query (UTF-8)
    const char* engines_csv,     // comma-separated engine whitelist, "" = defaults
    int limit,                   // max merged results (1–100)
    int offset,                  // skip first N results (0 = no skip)
    int rrf_k,                   // RRF smoothing constant (use 60 for default)
    const char* timeout_str,     // duration string, e.g. "8s"
    const char* weights_json     // JSON object: {"brave":2.0,"ddg":1.0}, "{}" = none
);
```

**Returns:** JSON matching the `format.Envelope` schema (§10):

```json
{
  "schema": 1,
  "query": "...",
  "took_ms": 1523,
  "limit": 5,
  "engines": {
    "ddg": {"ok": true, "count": 20, "took_ms": 1520, "error": ""},
    "google": {"ok": true, "count": 20, "took_ms": 453, "error": ""}
  },
  "results": [
    {
      "rank": 1,
      "title": "...",
      "url": "...",
      "source": "...",
      "snippet": "...",
      "score": 0.031,
      "engines": ["ddg", "google"]
    }
  ],
  "answer": ""
}
```

**Error handling:** On usage/config error (exit code 2), returns a structured
error object:

```json
{"error":{"code":"usage","message":"empty query","exit":2}}
```

### `MSTListEngines`

```c
char* MSTListEngines(void);
```

**Returns:** JSON array matching the `--list-engines` output format (each entry
has `name`, `enabled`, `keyed`, `key_present`, `weight`, `category`, `status`).

### `MSTFreeResult`

```c
void MSTFreeResult(char* ptr);
```

Frees a result string returned by `MSTSearch` or `MSTListEngines`.

## Python API (`mst`)

### `search(query, engines=None, limit=10, offset=0, rrf_k=60, timeout="8s", weights=None) → dict`

Execute a meta-search query.

| Parameter | Type | Default | Description |
|---|---|---|---|
| `query` | `str` | *(required)* | Search query |
| `engines` | `list[str] \| None` | `None` | Engine whitelist. `None` uses defaults (general category only: ddg, google). Use `["all"]` for every engine. See `list_engines()` for valid names. |
| `limit` | `int` | `10` | Max merged results to return (1–100) |
| `offset` | `int` | `0` | Skip first N merged results (pagination) |
| `rrf_k` | `int` | `60` | RRF smoothing constant; smaller = more top-heavy |
| `timeout` | `str` | `"8s"` | Global timeout, e.g. `"5s"`, `"500ms"` |
| `weights` | `dict[str, float] \| None` | `None` | Per-engine weight overrides, e.g. `{"brave": 2.0}` |

**Returns:** `dict` matching the `format.Envelope` schema (parsed JSON).

**Raises:** `MSTError` on engine failure (exit 3) or usage error (exit 2).

```python
import mst

# Basic search
result = mst.search("rust async runtime comparison")
for item in result["results"]:
    print(f"{item['rank']}. {item['title']} ({item['score']:.4f})")

# Multi-engine with weighting
result = mst.search(
    "tokio vs asyncio",
    engines=["ddg", "google", "brave"],
    weights={"brave": 2.0},
    limit=5,
)

# Academic search
result = mst.search(
    "vision transformer 2025",
    engines=["academic"],
)

# Pagination
result = mst.search("deep learning", offset=10, limit=5)
```

### `await search_async(query, engines=None, limit=10, offset=0, rrf_k=60, timeout="8s", weights=None) → dict`

Async wrapper around `search()`. It accepts and forwards the same arguments,
returns the same envelope, and raises the same `MSTError`. Implementation is a
thin `asyncio.to_thread(search, ...)` call — not a second search path.

```python
import asyncio
import mst

async def main():
    envelope = await mst.search_async("python asyncio", limit=5)
    print(envelope["results"])

asyncio.run(main())
```

**Thread safety:** concurrent `search()` and `search_async()` calls are
supported. The native CGO library is loaded with `ctypes.CDLL`, which releases
the GIL around `MSTSearch`/`MSTListEngines`; `asyncio.to_thread` uses the loop's
default executor (`min(32, os.cpu_count() + 4)` workers).

**Timeout/cancellation:** the `timeout` argument bounds the native Go search.
`asyncio.wait_for`/task cancellation cancels only the await; a worker thread may
finish in the background. A later call is unaffected. There is deliberately no
native cancellation handle in v0.5.

### `list_engines() → list[dict]`

List all registered engines with their operational state.

**Returns:** `list[dict]`, each entry:

| Field | Type | Description |
|---|---|---|
| `name` | `str` | Engine name (e.g. `"ddg"`, `"brave"`) |
| `enabled` | `bool` | Whether this engine is enabled by default |
| `keyed` | `bool` | Whether an API key is required |
| `key_present` | `bool \| None` | Key env var is set (null for keyless engines) |
| `weight` | `float` | Effective weight |
| `category` | `str` | Category: `"general"`, `"academic"`, `"reference"`, `"news"`, `"q&a"` |
| `status` | `str` | `"ready"`, `"missing-key"`, or `"disabled"` |

```python
import mst

for eng in mst.list_engines():
    print(f"{eng['name']:15s} {eng['status']:12s} weight={eng['weight']}")
```

### `MSTError` Exception Class

```python
class MSTError(Exception):
    """Raised when mst returns a non-zero exit code."""
    code: str      # "usage" | "config" | "no_results" | "engine_failure"
    message: str   # Human-readable error
    exit: int      # Exit code (0–3)
```

## Engine Selection Precedence

The engine selection follows the same rules as the Go CLI:

1. **Default mode** (no `engines` specified): only `"general"` category engines
   are enabled (`ddg`, `google`). Keyed engines like `brave` are auto-skipped
   if their API key env var is not set.
2. **Whitelist mode** (`engines=["brave","ddg"]`): only listed engines run.
3. **Category aliases**: `engines=["academic"]` expands to all academic-category
   engines (arxiv, pubmed). `engines=["all"]` enables every registered engine.
4. **Key resolution**: keyed engines check their env var at runtime. A missing
   key → status `missing-key` → auto-skipped (not an error).

### Supported Engines

| Tier | Engine | Key Required | Default Enabled | Category |
|------|--------|-------------|-----------------|----------|
| A | `ddg` | No | Yes | general |
| A | `google` | No | Yes | general |
| A | `brave` | `BRAVE_API_KEY` | No* | general |
| B | `wikipedia` | No | No | reference |
| B | `arxiv` | No | No | academic |
| B | `pubmed` | No | No | academic |
| B | `stackexchange` | No | No | q&a |
| B | `hackernews` | No | No | news |
| B | `grokipedia` | No | No | reference |
| A | `mojeek` | No | No | general |
| C | `bing` | `BING_API_KEY` | No* | general |
| D | `tavily` | `TAVILY_API_KEY` | No* | general |

*\*Enabled if key is present.*

## nanobot Integration

### Configuration

Add to `~/.nanobot/config.json`:

```json
{
  "tools": {
    "web": {
      "enable": true,
      "search": {
        "provider": "mst",
        "max_results": 10,
        "timeout": 30,
        "engines": ["all"],
        "weights": {"brave": 2.0}
      }
    }
  }
}
```

### nanobot `WebSearchConfig` Extension

The existing `WebSearchConfig` in `nanobot/agent/tools/web.py` gains two new fields:

```python
class WebSearchConfig(Base):
    provider: str = "duckduckgo"
    api_key: str = ""
    base_url: str = ""
    max_results: int = 5
    timeout: int = 30
    # mst-specific fields (only used when provider == "mst")
    mst_engines: list[str] | None = None     # engine whitelist; None = defaults
    mst_weights: dict[str, float] | None = None  # per-engine weight overrides
```

### nanobot `WebSearchTool` Provider Branch

Add to `SEARCH_PROVIDER_OPTIONS`:

```python
{"name": "mst", "label": "Meta Search Tool (mst)", "credential": "none"},
```

Add `_search_mst()` method:

```python
async def _search_mst(self, query: str, n: int) -> str:
    try:
        import mst  # pyright: ignore[reportMissingImports]
    except ImportError:
        return ToolResult.error(
            "Error: mst-python not installed. Run: pip install mst-python"
        )
    try:
        result = mst.search(
            query=query,
            engines=self.config.mst_engines or None,
            limit=n,
            timeout=f"{self.config.timeout}s",
            weights=self.config.mst_weights,
        )
        items = [
            {
                "title": r["title"],
                "url": r["url"],
                "content": r.get("snippet", ""),
            }
            for r in result.get("results", [])
        ]
        return _format_results(query, items, n)
    except mst.MSTError as e:
        return ToolResult.error(f"mst search error ({e.code}): {e.message}")
```

Add routing in `execute()`:

```python
if provider == "mst":
    return await self._search_mst(query, n)
```

### nanobot Dependency

Add to `pyproject.toml` optional dependencies:

```toml
[project.optional-dependencies]
mst = [
    "mst-python>=0.1,<1.0",
]
```

### nanobot Config Wizard

The existing config wizard (which uses `SEARCH_PROVIDER_OPTIONS`) will
automatically surface `"mst"` as a provider option with `credential: "none"`.

## Error Mapping

| Go Exit Code | Python Exception | nanobot Result |
|-------------|-----------------|----------------|
| 0 | Success → parsed dict | Formatted result string |
| 1 (no results) | `MSTError(code="no_results")` | `"No results for: <query>"` |
| 2 (usage error) | `MSTError(code="usage", message=...)` | `"Error: mst search error (usage): ..."` |
| 3 (engine failure) | `MSTError(code="engine_failure")` | `"Error: mst search error (engine_failure): ..."` |

## Build Dependencies

| Component | Dependency | Version |
|---|---|---|
| Python | Cython | >=3.0 |
| Python | setuptools / build | >=70.0 |
| Go | go | >=1.26 |
| CGO | gcc (Linux) / clang (macOS) | system default |

### Local Build

```bash
cd python
pip install build cython
CGO_ENABLED=1 python -m build --wheel
```

The `CGO_ENABLED=1` env var tells the Go toolchain to compile CGO code. On macOS
this requires Xcode command-line tools (`xcode-select --install`). On Linux,
requires `gcc`.

### CI/CD (PyPI Publishing)

The `.github/workflows/publish-python.yml` workflow builds wheels for:
- **Linux x86_64**: Python 3.11, 3.12, 3.13
- **macOS Intel** (macos-13): Python 3.11, 3.12, 3.13
- **macOS ARM64** (macos-14): Python 3.11, 3.12, 3.13

Triggered by pushing a `py-v*` tag (e.g. `py-v0.1.0`). Uploads to PyPI via
twine with the `PYPI_API_TOKEN` secret.

**Tag convention:** Use `v0.3.2` for the Go binary release and `py-v0.3.2` for
the Python wheel release — they share the same version number but use different
prefixes to avoid conflicts.

## Testing

The extension ships with two test suites:

1. **Go tests** — existing `*_test.go` files cover engine implementations, ranking,
   and format output. The CGO bridge adds tests in `cgo/mst_cgo_test.go`.
2. **Python tests** — `python/tests/` contains integration tests that verify:
   - `search()` returns valid parsed JSON matching the envelope schema
   - Engine selection and weighting work correctly
   - Error cases produce correct exception types and messages
   - `list_engines()` returns all 14 engines with correct metadata

Tests run via: `pytest python/tests/`

## Future Extensions

- **`search_async()`** — async variant using Python's `asyncio.to_thread()` wrapper
  around the synchronous CGO call (releases GIL during Go execution).
- **Streaming** — expose per-engine results as they arrive (requires modifying the
  dispatch loop to emit partial results).
- **Config file support** — read `$MST_CONFIG_HOME/config.yaml` or
  `~/.config/mst/config.yaml` for persistent engine weights and timeouts.
- **Tavily answer extraction** — expose the AI-generated summary from Tavily as a
  separate field in the result envelope.
