"""End-to-end tests for the mst Python (CGO) binding.

These load the compiled _mst.so (Go c-shared) + the Cython extension and exercise
the binding the way a user would. The extension must be built first:

    cd python && python setup.py build_ext --inplace
    # (or: pip install -e python/)

Tests that hit the network are gated behind MST_ITEST=1, matching the Go suite's
convention (spec §14).
"""

import asyncio
import ctypes
import os
import threading
import time

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
    for n in ("ddg", "google", "wikipedia", "brave", "europepmc", "jina"):
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
    try:
        res = mst.search("python", engines=["wikipedia"], limit=3, timeout="15s")
    except mst.MSTError as exc:
        pytest.skip(f"live search unavailable: {exc}")
    assert len(res["results"]) <= 3


# --- async API (deterministic) ----------------------------------------------


def test_search_async_forwards_arguments_and_returns_same_shape(monkeypatch):
    calls = []

    def fake(*args, **kwargs):
        calls.append((args, kwargs))
        return {"schema": 1, "query": "q", "results": []}

    monkeypatch.setattr(mst, "search", fake)
    got = asyncio.run(
        mst.search_async(
            "q",
            engines=["wikipedia"],
            limit=3,
            offset=2,
            rrf_k=42,
            timeout="5s",
            weights={"wikipedia": 2.0},
        )
    )
    assert got == {"schema": 1, "query": "q", "results": []}
    assert calls == [
        (
            ("q",),
            {
                "engines": ["wikipedia"],
                "limit": 3,
                "offset": 2,
                "rrf_k": 42,
                "timeout": "5s",
                "weights": {"wikipedia": 2.0},
            },
        )
    ]


def test_search_async_propagates_mst_error(monkeypatch):
    def fake(*args, **kwargs):
        raise mst.MSTError("usage", "bad query", 2)

    monkeypatch.setattr(mst, "search", fake)
    with pytest.raises(mst.MSTError) as exc:
        asyncio.run(mst.search_async("q"))
    assert exc.value.code == "usage"
    assert exc.value.exit_code == 2


def test_search_async_runs_on_worker_thread(monkeypatch):
    def fake(*args, **kwargs):
        return {"thread": threading.current_thread().name}

    monkeypatch.setattr(mst, "search", fake)
    got = asyncio.run(mst.search_async("q"))
    assert got["thread"] != "MainThread"


def test_loader_is_ctypes_cdll():
    from mst import _mst

    assert type(_mst._lib) is ctypes.CDLL, "ctypes.CDLL is required to release the GIL"


def test_search_async_event_loop_stays_responsive(monkeypatch):
    started = threading.Event()
    release = threading.Event()

    def fake(*args, **kwargs):
        started.set()
        release.wait(2)
        return {"ok": True}

    monkeypatch.setattr(mst, "search", fake)

    async def run():
        task = asyncio.create_task(mst.search_async("q"))
        await asyncio.to_thread(started.wait, 1)
        ticks = 0
        while not task.done():
            ticks += 1
            await asyncio.sleep(0.005)
        release.set()
        await task
        return ticks

    ticks = asyncio.run(run())
    assert ticks > 0, "event loop did not advance while the search was in flight"


def test_search_async_concurrent_calls_overlap(monkeypatch):
    lock = threading.Lock()
    active = 0
    max_active = 0

    def fake(*args, **kwargs):
        nonlocal active, max_active
        with lock:
            active += 1
            max_active = max(max_active, active)
        time.sleep(0.05)
        with lock:
            active -= 1
        return {"ok": True}

    monkeypatch.setattr(mst, "search", fake)

    async def run():
        return await asyncio.gather(*(mst.search_async(str(i)) for i in range(4)))

    results = asyncio.run(run())
    assert len(results) == 4
    assert max_active >= 2, "search_async calls were serialized instead of overlapping"


def test_search_async_cancellation_does_not_block_next_call(monkeypatch):
    release = threading.Event()

    def slow(*args, **kwargs):
        release.wait(2)
        return {"slow": True}

    monkeypatch.setattr(mst, "search", slow)

    async def run():
        with pytest.raises(asyncio.TimeoutError):
            await asyncio.wait_for(mst.search_async("q"), timeout=0.01)
        release.set()

    asyncio.run(run())

    monkeypatch.setattr(mst, "search", lambda *a, **k: {"fast": True})
    assert asyncio.run(mst.search_async("q")) == {"fast": True}


# --- async API (live, gated) ------------------------------------------------


@LIVE
def test_search_async_live_loop_progress():
    async def run():
        task = asyncio.create_task(
            mst.search_async("golang", engines=["wikipedia"], limit=2, timeout="15s")
        )
        ticks = 0
        while not task.done():
            ticks += 1
            await asyncio.sleep(0.001)
        return ticks, await task

    try:
        ticks, res = asyncio.run(run())
    except mst.MSTError as exc:
        pytest.skip(f"live search unavailable: {exc}")

    assert isinstance(res, dict)
    assert ticks > 0
    print(f"event-loop ticks during live search: {ticks}")


@LIVE
def test_search_async_live_concurrent():
    queries = ["golang", "rust", "python"]

    async def run():
        return await asyncio.gather(
            *(
                mst.search_async(q, engines=["wikipedia"], limit=2, timeout="15s")
                for q in queries
            )
        )

    try:
        results = asyncio.run(run())
    except mst.MSTError as exc:
        pytest.skip(f"live concurrent search unavailable: {exc}")

    assert len(results) == len(queries)
    for res in results:
        assert isinstance(res, dict)
        assert isinstance(res.get("results"), list)
