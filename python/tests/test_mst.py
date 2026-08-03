"""End-to-end tests for the mst Python (CGO) binding.

These load the compiled _mst.so (Go c-shared) + the Cython extension and exercise
the binding the way a user would. The extension must be built first:

    cd python && python setup.py build_ext --inplace
    # (or: pip install -e python/)

Tests that hit the network are gated behind MST_ITEST=1, matching the Go suite's
convention (spec §14).
"""

import os

import pytest

try:
    import mst
except (ImportError, OSError) as exc:  # extension not built, or Go _mst.so missing
    pytest.skip(
        f"mst extension not importable: {exc}. "
        "Build it first: `python setup.py build_ext --inplace` in python/",
        allow_module_level=True,
    )


# --- list_engines (deterministic: no keys, no network) ----------------------


def test_list_engines_returns_list_of_dicts():
    engines = mst.list_engines()
    assert isinstance(engines, list)
    assert engines, "expected at least one engine"
    for e in engines:
        assert isinstance(e, dict)


def test_list_engines_has_expected_fields():
    required = {"name", "enabled", "keyed", "weight", "status", "category"}
    for e in mst.list_engines():
        missing = required - set(e)
        assert not missing, f"engine {e.get('name')!r} missing fields {missing}"


def test_list_engines_includes_known_engines():
    names = {e["name"] for e in mst.list_engines()}
    # general keyless engines shipped enabled by default, plus a keyed one.
    for n in ("ddg", "google", "wikipedia", "brave"):
        assert n in names, f"missing expected engine {n!r}"


def test_list_engines_statuses_valid():
    valid = {"ready", "disabled", "missing-key"}
    for e in mst.list_engines():
        assert e["status"] in valid, f"{e['name']!r} has bad status {e['status']!r}"


def test_list_engines_keyed_have_key_present():
    for e in mst.list_engines():
        if e["keyed"]:
            assert e.get("key_present") is not None, f"keyed engine {e['name']!r} lacks key_present"


# --- error paths (deterministic: no network) --------------------------------


def test_search_empty_query_raises_usage():
    with pytest.raises(mst.MSTError) as exc:
        mst.search("   ")
    assert exc.value.code == "usage"
    assert exc.value.exit_code == 2


def test_search_invalid_timeout_raises_usage():
    with pytest.raises(mst.MSTError) as exc:
        mst.search("go", timeout="not-a-duration")
    assert exc.value.code == "usage"
    assert exc.value.exit_code == 2


def test_mst_error_carries_fields_and_str():
    try:
        mst.search("")
    except mst.MSTError as e:
        assert isinstance(e.code, str) and e.code
        assert isinstance(e.exit_code, int)
        assert isinstance(e.message, str)
        assert "mst error" in str(e)
        return
    pytest.fail("expected MSTError for empty query")


# --- live search (gated: needs network; spec §14 MST_ITEST convention) ------


LIVE = pytest.mark.skipif(
    not os.environ.get("MST_ITEST"),
    reason="set MST_ITEST=1 to run live tests (spec §14)",
)


@LIVE
def test_search_live_returns_envelope_contract():
    res = mst.search(
        "rust async runtime",
        engines=["ddg", "google", "wikipedia"],
        limit=5,
        timeout="15s",
    )
    assert isinstance(res, dict)
    assert res["query"] == "rust async runtime"
    assert isinstance(res["results"], list)
    assert isinstance(res.get("engines"), dict)
    for r in res["results"]:
        assert {"rank", "title", "url"} <= set(r), f"result missing fields: {r}"


@LIVE
def test_search_live_limit_is_respected():
    res = mst.search("python", engines=["ddg"], limit=3, timeout="15s")
    assert len(res["results"]) <= 3
