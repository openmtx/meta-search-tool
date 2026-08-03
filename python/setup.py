"""Setup script for mst-python.

Builds the CGO shared library from the Go codebase, then compiles Cython bindings.
"""

import os
import subprocess
import sys
from pathlib import Path

from Cython.Build import cythonize
from setuptools import Extension, setup

CGO_DIR = Path(__file__).parent.parent / "cgo"
PYTHON_SRC = Path(__file__).parent / "src"
CGO_LIB = PYTHON_SRC / "mst" / "_mst.so"


def build_cgo():
    """Build the CGO shared library from the Go codebase."""
    if CGO_LIB.exists():
        return

    print(f"[mst] Building CGO library from {CGO_DIR}...")

    env = os.environ.copy()
    env["CGO_ENABLED"] = "1"

    result = subprocess.run(
        ["go", "build", "-buildmode=c-shared", "-o", str(CGO_LIB), "."],
        cwd=str(CGO_DIR),
        env=env,
        capture_output=True,
        text=True,
    )

    if result.returncode != 0:
        print(f"[mst] CGO build failed:\n{result.stderr}", file=sys.stderr)
        sys.exit(1)

    print(f"[mst] CGO library built at {CGO_LIB}")


build_cgo()

extensions = [
    Extension(
        "mst._mst",
        sources=[str(PYTHON_SRC / "mst" / "_mst.pyx")],
        include_dirs=[str(PYTHON_SRC / "mst")],
        extra_link_args=[
            f"-Wl,-rpath,$ORIGIN",
            str(CGO_LIB),
        ],
    ),
]

setup(
    ext_modules=cythonize(extensions, language_level="3"),
)
