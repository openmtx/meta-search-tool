# cython: language_level=3
"""Cython bindings to the mst CGO shared library."""

import ctypes
import json
import os
import platform as _platform
from typing import Any


# --- Load the CGO shared library ---

# Go's -buildmode=c-shared produces .so on Linux/macOS, .dll on Windows.
if _platform.system() == "Windows":
    _cgo_path = os.path.join(os.path.dirname(__file__), "_mst.dll")
else:
    _cgo_path = os.path.join(os.path.dirname(__file__), "_mst.so")
# ctypes.CDLL releases the GIL around foreign calls. This is required for the
# async API: `search_async()` offloads MSTSearch to a worker thread while the
# event loop keeps running. Do not switch to PyDLL (or a binding that holds the
# GIL), or every asyncio loop will block for the duration of a search.
_lib = ctypes.CDLL(_cgo_path)

# Set up function signatures — use c_void_p to preserve Go-allocated pointers
_lib.MSTSearch.argtypes = [
    ctypes.c_char_p,  # query
    ctypes.c_char_p,  # engines_csv
    ctypes.c_int,     # limit
    ctypes.c_int,     # offset
    ctypes.c_int,     # rrf_k
    ctypes.c_char_p,  # timeout_str
    ctypes.c_char_p,  # weights_json
]
_lib.MSTSearch.restype = ctypes.c_void_p

_lib.MSTListEngines.argtypes = []
_lib.MSTListEngines.restype = ctypes.c_void_p

_lib.MSTFreeResult.argtypes = [ctypes.c_void_p]
_lib.MSTFreeResult.restype = None


def _call_cgo(func, *args):
    """Call a CGO function and return parsed JSON, or raise MSTError."""
    result_ptr = func(*args)
    try:
        if not result_ptr:
            raise MSTError("internal", "CGO returned NULL", 2)
        # ctypes.c_void_p returns an int; get the C string via c_char_p
        c_str = ctypes.c_char_p(result_ptr)
        data = c_str.value  # reads until null byte
        if data is None:
            raise MSTError("internal", "CGO returned empty string", 2)
        return json.loads(data)
    finally:
        _lib.MSTFreeResult(result_ptr)


class MSTError(Exception):
    """Raised when mst returns a non-zero exit code."""

    def __init__(self, code: str, message: str, exit_code: int):
        self.code = code
        self.message = message
        self.exit_code = exit_code
        super().__init__(f"mst error ({code}): {message}")


def search(
    query: str,
    engines=None,
    limit: int = 10,
    offset: int = 0,
    rrf_k: int = 60,
    timeout: str = "8s",
    weights=None,
) -> dict:
    """Execute a meta-search query via CGO.

    Args:
        query: Search query string.
        engines: Comma-separated engine whitelist or None for defaults.
        limit: Max merged results (1-100).
        offset: Skip first N results.
        rrf_k: RRF smoothing constant (use 60 for default).
        timeout: Duration string, e.g. "8s".
        weights: Per-engine weight overrides as dict, e.g. {"brave": 2.0}.

    Returns:
        Dict matching the format.Envelope schema.

    Raises:
        MSTError: On usage error (exit 2), no results (exit 1), or engine failure (exit 3).
    """
    engines_csv = ",".join(engines) if engines else ""
    weights_json = json.dumps(weights).encode() if weights else b"{}"

    result = _call_cgo(
        _lib.MSTSearch,
        query.encode(),
        engines_csv.encode(),
        limit,
        offset,
        rrf_k,
        timeout.encode(),
        weights_json,
    )

    # Check for error envelope
    if "error" in result:
        err = result["error"]
        raise MSTError(err["code"], err["message"], err["exit"])

    return result


def list_engines() -> list[dict]:
    """List all registered engines with their operational state.

    Returns:
        List of dicts, each with name, enabled, keyed, key_present, weight,
        category, and status fields.
    """
    result = _call_cgo(_lib.MSTListEngines)
    if isinstance(result, list):
        return result
    # Some versions may wrap in an envelope
    if isinstance(result, dict) and "engines" in result:
        return result["engines"]
    raise MSTError("internal", "unexpected list_engines response format", 2)
