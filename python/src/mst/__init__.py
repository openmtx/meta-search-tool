"""mst: CGO Python bindings for mst (Meta-Search Tool).

Concurrent multi-engine web search with RRF ranking, zero subprocess overhead.

Usage:
    import mst

    result = mst.search("rust async runtime comparison")
    for item in result["results"]:
        print(f"{item['rank']}. {item['title']}")

    engines = mst.list_engines()
"""

from ._mst import MSTError, list_engines, search

__all__ = ["search", "list_engines", "MSTError"]

# Reflect the installed distribution version (populated by setup.py from the
# release tag). Falls back when run from a source tree that isn't installed.
try:
    from importlib.metadata import PackageNotFoundError, version as _dist_version

    try:
        __version__ = _dist_version("mst")
    except PackageNotFoundError:
        __version__ = "0.0.0-dev"
except ImportError:  # Python <3.8 (unsupported, but stay safe)
    __version__ = "0.0.0-dev"
