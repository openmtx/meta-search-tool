"""Setup script for mst-python.

Builds the CGO shared library from the Go codebase, then compiles Cython bindings.
"""

import os
import subprocess
import sys
from pathlib import Path

from Cython.Build import cythonize
from setuptools import Extension, setup

import platform as _platform

CGO_DIR = Path(__file__).parent.parent / "cgo"
PYTHON_SRC = Path(__file__).parent / "src"

# Platform-specific shared library name for Go's -buildmode=c-shared.
# Linux/macOS: .so (ELF/DYLIB). Windows: .dll (PE/COFF).
if _platform.system() == "Windows":
    CGO_LIB = PYTHON_SRC / "mst" / "_mst.dll"
    CGO_LIB_REL = "_mst.dll"
else:
    CGO_LIB = PYTHON_SRC / "mst" / "_mst.so"
    CGO_LIB_REL = "_mst.so"


def resolve_version():
    """Resolve the package version from the release tag.

    The git tag is the single source of truth for BOTH the CLI (stamped into the
    binary via `-ldflags -X main.version=$tag` in release.yml) and this Python
    package, so the two can never drift. Precedence:

      1. MST_VERSION env var (set by CI to the tag) — reliable in a shallow
         checkout where `git describe` may have nothing to read.
      2. `git describe --tags --abbrev=0 --match 'v[0-9]*'` — nearest version
         tag, for local builds.
      3. "0.0.0-dev" — fresh clone with no tags.

    A leading 'v' is stripped: Go modules / the CLI use 'v0.4.0', but PyPI /
    PEP 440 require '0.4.0'.
    """
    raw = os.environ.get("MST_VERSION", "").strip()
    if not raw:
        try:
            out = subprocess.run(
                ["git", "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*"],
                capture_output=True, text=True, check=True,
            )
            raw = out.stdout.strip()
        except (subprocess.CalledProcessError, FileNotFoundError):
            raw = ""
    if not raw:
        return "0.0.0-dev"
    return raw[1:] if raw.startswith("v") else raw


def build_cgo():
    """Build the CGO shared library from the Go codebase."""
    if CGO_LIB.exists():
        return

    print(f"[mst] Building CGO library from {CGO_DIR}...")

    env = os.environ.copy()
    env["CGO_ENABLED"] = "1"

    # On Windows, the default CC is MSYS2 MinGW which produces ELF binaries.
    # Force Go to use the MSVC toolchain so the CGO lib is a PE DLL that
    # link.exe can consume. Set CC=cl and pass the VS include path via
    # CGO_CFLAGS.
    if _platform.system() == "Windows":
        env["CC"] = "cl"
        # Find MSVC include dir: scan for the first VC/Tools/MSVC/*/include
        vs_base = Path(env.get("PROGRAMFILES", r"C:\Program Files"))
        vc_path = vs_base / "Microsoft Visual Studio" / "18" / "Enterprise" / "VC"
        if vc_path.exists():
            msvc_ver = next(vc_path.glob("Tools/MSVC/*/"), None)
            if msvc_ver:
                env["CGO_CFLAGS"] = f"-I{msvc_ver.parent / 'include'}"
                env["CGO_LDFLAGS"] = f"-LIBPATH:{msvc_ver.parent / 'lib/x64'}"

    result = subprocess.run(
        [
            "go", "build",
            # Disable VCS stamping: we don't embed git info in the .so, and Go's
            # buildvcs fails (exit 128) in environments where git can't read .git
            # (e.g. the manylinux build container, or CI checkouts with foreign
            # .git ownership).
            "-buildvcs=false",
            "-buildmode=c-shared",
            "-o", str(CGO_LIB),
            ".",
        ],
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
        # setuptools (PEP 517 build) requires source/include paths to be
        # relative to setup.py's directory — absolute paths are rejected.
        sources=["src/mst/_mst.pyx"],
        include_dirs=["src/mst"],
        extra_link_args=[
            "-Wl,-rpath,$ORIGIN",
            str(CGO_LIB),
        ] if _platform.system() != "Windows" else [str(CGO_LIB)],
    ),
]

setup(
    version=resolve_version(),
    # Ship the Go c-shared lib inside the package so ctypes can load it
    # at runtime; drop the generated Cython C (not needed after compilation).
    package_data={"mst": [CGO_LIB_REL]},
    exclude_package_data={"mst": ["_mst.c"]},
    ext_modules=cythonize(extensions, language_level="3"),
)
