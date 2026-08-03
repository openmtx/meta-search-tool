"""pytest bootstrap.

Prefers an already-importable `mst` (an installed wheel or `pip install -e .` —
the CI path) and only falls back to adding the in-place `src/` to sys.path for
local dev without an install. Either way `import mst` works whether you
`pip install` the wheel, do an editable install, or `python setup.py
build_ext --inplace`.
"""
import os
import sys

try:
    import mst  # noqa: F401 — already importable (installed wheel / editable install)
except ImportError:
    _SRC = os.path.join(os.path.dirname(__file__), "..", "src")
    if os.path.isdir(_SRC):
        sys.path.insert(0, os.path.abspath(_SRC))
