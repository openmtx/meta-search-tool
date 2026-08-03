# mst-python — Meta Search Tool (Python bindings)

[![Release](https://github.com/openmtx/meta-search-tool/actions/workflows/release.yml/badge.svg)](https://github.com/openmtx/meta-search-tool/actions/workflows/release.yml)
[![PyPI](https://img.shields.io/pypi/v/mst-python)](https://pypi.org/project/mst-python/)
[![Python Versions](https://img.shields.io/pypi/pyversions/mst-python)](https://pypi.org/project/mst-python/)

CGO Python bindings for **mst** — a concurrent multi-engine web search tool with
transparent RRF ranking. Fan out to 12+ engines in parallel, get merged JSON
results with scores. No server, no setup.

```python
import mst

# Search across all enabled engines
result = mst.search("rust async runtime comparison")

# Or pick specific engines
result = mst.search("riscv paging", engines=["ddg", "google"], limit=10)

print(f"Found {len(result['results'])} results in {result['took_ms']}ms")
for r in result['results']:
    print(f"  #{r['rank']}  {r['title']}  (score={r['score']})")
```

## Features

- **12 engines** across general web, academic, reference, Q&A, and AI categories
- **Concurrent fan-out** with per-engine timeouts — a slow engine never blocks the run
- **RRF ranking** with configurable per-engine weights and smoothing constant
- **Keyless engines** work out of the box: DuckDuckGo, Google, Wikipedia, arXiv, and more
- **Keyed engines** (Brave, Bing, Tavily) ready when you set their API keys

## Installation

```bash
pip install mst-python
```

Works on Linux (manylinux_2_34 x86_64) and macOS (Apple Silicon arm64).

## Quick start

```python
import mst

# List all engines and their status
engines = mst.list_engines()
for e in engines:
    print(f"{e['name']:20s}  {e['status']:15s}  weight={e['weight']}")

# Run a search
result = mst.search("python asyncio vs threading", limit=5)
print(result)
```

## API

### `mst.search(query, engines=None, limit=10, offset=0, rrf_k=60, timeout="8s", weights=None)`

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `query` | `str` | — | Search query |
| `engines` | `list[str]` or `None` | `None` | Engine whitelist (`None` = all enabled engines) |
| `limit` | `int` | `10` | Max merged results (1–100) |
| `offset` | `int` | `0` | Skip first N results |
| `rrf_k` | `int` | `60` | RRF smoothing constant |
| `timeout` | `str` | `"8s"` | Per-engine timeout duration |
| `weights` | `dict[str, float]` or `None` | `None` | Per-engine weight overrides |

Returns a dict with `query`, `took_ms`, `results` (list of ranked results).

### `mst.list_engines()`

Returns a list of engine dicts with `name`, `enabled`, `keyed`, `weight`, `category`, `status`.

### `mst.__version__`

Package version string (derived from the release tag when installed via PyPI).

## Requirements

- Python ≥ 3.11
- Linux (x86_64, glibc ≥ 2.34) or macOS (arm64)

No runtime dependencies — the CGO shared library is bundled in the wheel.

## License

Apache-2.0. See [`LICENSE`](https://github.com/openmtx/meta-search-tool/blob/master/LICENSE).
