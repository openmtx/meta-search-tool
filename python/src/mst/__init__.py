"""mst: CGO Python bindings for mst (Meta-Search Tool).

Concurrent multi-engine web search with RRF ranking, zero subprocess overhead.

Usage:
    import mst

    result = mst.search("rust async runtime comparison")
    for item in result["results"]:
        print(f"{item['rank']}. {item['title']}")

    engines = mst.list_engines()
"""

import asyncio

from ._mst import MSTError, list_engines, search

__all__ = ["search", "search_async", "list_engines", "MSTError"]


async def search_async(
    query: str,
    engines=None,
    limit: int = 10,
    offset: int = 0,
    rrf_k: int = 60,
    timeout: str = "8s",
    weights=None,
) -> dict:
    """Async wrapper around :func:`search`.

    The native CGO call is blocking (the Go runtime releases the GIL while it
    runs), so this thin wrapper offloads it to the event loop's default executor
    via :func:`asyncio.to_thread`. It returns the same envelope and raises the
    same :class:`MSTError` as :func:`search`.

    This is intentionally *not* a second search implementation: every argument
    is forwarded unchanged.
    """
    return await asyncio.to_thread(
        search,
        query,
        engines=engines,
        limit=limit,
        offset=offset,
        rrf_k=rrf_k,
        timeout=timeout,
        weights=weights,
    )

# Reflect the installed distribution version (populated by setup.py from the
# release tag). Falls back when run from a source tree that isn't installed.
try:
    from importlib.metadata import PackageNotFoundError, version as _dist_version

    # The PyPI distribution is `mst-python` while the import package is `mst`;
    # accept either name so editable/source installs and wheels both work.
    for _dist in ("mst-python", "mst"):
        try:
            __version__ = _dist_version(_dist)
            break
        except PackageNotFoundError:
            continue
    else:
        __version__ = "0.0.0-dev"
except ImportError:  # Python <3.8 (unsupported, but stay safe)
    __version__ = "0.0.0-dev"
