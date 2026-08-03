"""pytest bootstrap.

Makes the in-place-built `mst` package importable without requiring `pip install`
(so `cd python && pytest` works after `python setup.py build_ext --inplace`).
When installed via `pip install -e .` this is a no-op, since `mst` is already on
sys.path.
"""
import os
import sys

_SRC = os.path.join(os.path.dirname(__file__), "..", "src")
if os.path.isdir(_SRC):
    sys.path.insert(0, os.path.abspath(_SRC))
