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
__version__ = "0.1.0"
