"""Unit tests for cocoindex/mcp_server.py — all external deps are mocked."""

import json
import os
import sys
import time
import types
from unittest import mock

import pytest

# ---------------------------------------------------------------------------
# Stub numpy + cocoindex before importing mcp_server.py
# ---------------------------------------------------------------------------

_np = types.ModuleType("numpy")
_np.float32 = "float32"
_nptyping = types.ModuleType("numpy.typing")
_nptyping.NDArray = type("NDArray", (), {"__class_getitem__": classmethod(lambda cls, x: cls)})
_np.typing = _nptyping
sys.modules["numpy"] = _np
sys.modules["numpy.typing"] = _nptyping

_ci = types.ModuleType("cocoindex")


def _transform_flow_factory():
    def decorator(fn):
        fn.transform = fn
        return fn
    return decorator


_ci.transform_flow = _transform_flow_factory
_ci.DataSlice = type("DataSlice", (), {"__class_getitem__": classmethod(lambda cls, x: cls)})
_funcs = types.ModuleType("cocoindex.functions")
_funcs.SentenceTransformerEmbed = lambda model="": None
_ci.functions = _funcs
sys.modules["cocoindex"] = _ci
sys.modules["cocoindex.functions"] = _funcs

# Stub psycopg2 + sentence_transformers
_psycopg2 = types.ModuleType("psycopg2")
_psycopg2.connect = mock.MagicMock()
sys.modules["psycopg2"] = _psycopg2

_st = types.ModuleType("sentence_transformers")
_st.SentenceTransformer = mock.MagicMock()
sys.modules["sentence_transformers"] = _st

# Stub mcp.server.fastmcp
_mcp = types.ModuleType("mcp")
_mcp_server_mod = types.ModuleType("mcp.server")
_mcp_fastmcp = types.ModuleType("mcp.server.fastmcp")


class _FakeFastMCP:
    def __init__(self, name="", **kwargs):
        self.name = name
        self._tools = {}

    def tool(self):
        def decorator(fn):
            self._tools[fn.__name__] = fn
            return fn
        return decorator

    def run(self, **kwargs):
        pass


_mcp_fastmcp.FastMCP = _FakeFastMCP
_mcp.server = _mcp_server_mod
_mcp_server_mod.fastmcp = _mcp_fastmcp
sys.modules["mcp"] = _mcp
sys.modules["mcp.server"] = _mcp_server_mod
sys.modules["mcp.server.fastmcp"] = _mcp_fastmcp

from mcp_server import (  # noqa: E402
    REFRESH_COOLDOWN_SEC,
    ServerConfig,
    create_server,
    embed_query,
    get_deleted_files,
    get_overlay_status,
    load_server_config,
    merge_results,
    query_table,
    refresh_overlay,
    search,
)


# ===================================================================
# ServerConfig / load_server_config
# ===================================================================


class TestLoadServerConfig:
    def test_loads_from_env(self):
        env = {
            "COCOINDEX_DATABASE_URL": "postgresql://localhost/cocoindex",
            "COCOINDEX_ENGINE_ID": "eng-abc123",
            "COCOINDEX_MAIN_TABLE": "main_backend_embeddings",
            "COCOINDEX_OVERLAY_TABLE": "ovl_eng_abc123",
            "COCOINDEX_TRACK": "backend",
            "COCOINDEX_WORKTREE": "/path/to/worktree",
        }
        with mock.patch.dict(os.environ, env, clear=False):
            cfg = load_server_config()
        assert cfg.database_url == "postgresql://localhost/cocoindex"
        assert cfg.engine_id == "eng-abc123"
        assert cfg.main_table == "main_backend_embeddings"
        assert cfg.overlay_table == "ovl_eng_abc123"
        assert cfg.track == "backend"
        assert cfg.worktree == "/path/to/worktree"

    def test_missing_database_url_raises(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            with pytest.raises(ValueError, match="COCOINDEX_DATABASE_URL"):
                load_server_config()

    def test_optional_env_vars_default_to_none(self):
        env = {"COCOINDEX_DATABASE_URL": "postgresql://localhost/db"}
        with mock.patch.dict(os.environ, env, clear=True):
            cfg = load_server_config()
        assert cfg.engine_id is None
        assert cfg.main_table is None
        assert cfg.overlay_table is None
        assert cfg.track is None
        assert cfg.worktree is None

    def test_empty_string_treated_as_none(self):
        env = {
            "COCOINDEX_DATABASE_URL": "postgresql://localhost/db",
            "COCOINDEX_ENGINE_ID": "",
            "COCOINDEX_OVERLAY_TABLE": "",
        }
        with mock.patch.dict(os.environ, env, clear=True):
            cfg = load_server_config()
        assert cfg.engine_id is None
        assert cfg.overlay_table is None

    def test_comma_separated_main_tables(self):
        env = {
            "COCOINDEX_DATABASE_URL": "postgresql://localhost/db",
            "COCOINDEX_MAIN_TABLE": "main_backend_embeddings,main_frontend_embeddings",
        }
        with mock.patch.dict(os.environ, env, clear=True):
            cfg = load_server_config()
        assert cfg.main_tables == ["main_backend_embeddings", "main_frontend_embeddings"]
        assert cfg.main_table == "main_backend_embeddings"  # first table as fallback

    def test_single_table_no_main_tables(self):
        env = {
            "COCOINDEX_DATABASE_URL": "postgresql://localhost/db",
            "COCOINDEX_MAIN_TABLE": "main_backend_embeddings",
        }
        with mock.patch.dict(os.environ, env, clear=True):
            cfg = load_server_config()
        assert cfg.main_table == "main_backend_embeddings"
        assert cfg.main_tables is None


# ===================================================================
# merge_results
# ===================================================================


class TestMergeResults:
    def test_main_only(self):
        main = [
            {"filename": "a.go", "location": "0:0", "code": "main", "score": 0.9},
            {"filename": "b.go", "location": "0:0", "code": "other", "score": 0.8},
        ]
        result = merge_results(main, [], [], top_k=10)
        assert len(result) == 2
        assert result[0]["filename"] == "a.go"

    def test_overlay_only(self):
        overlay = [
            {"filename": "c.go", "location": "0:0", "code": "overlay", "score": 0.95},
        ]
        result = merge_results([], overlay, [], top_k=10)
        assert len(result) == 1
        assert result[0]["code"] == "overlay"

    def test_overlay_wins_on_conflict(self):
        main = [
            {"filename": "a.go", "location": "0:0", "code": "old version", "score": 0.9},
        ]
        overlay = [
            {"filename": "a.go", "location": "0:0", "code": "new version", "score": 0.85},
        ]
        result = merge_results(main, overlay, [], top_k=10)
        assert len(result) == 1
        assert result[0]["code"] == "new version"
        assert result[0]["score"] == 0.85

    def test_deleted_files_excluded(self):
        main = [
            {"filename": "a.go", "location": "0:0", "code": "kept", "score": 0.9},
            {"filename": "deleted.go", "location": "0:0", "code": "gone", "score": 0.8},
        ]
        result = merge_results(main, [], ["deleted.go"], top_k=10)
        assert len(result) == 1
        assert result[0]["filename"] == "a.go"

    def test_sorted_by_score_descending(self):
        main = [
            {"filename": "a.go", "location": "0:0", "code": "a", "score": 0.7},
            {"filename": "b.go", "location": "0:0", "code": "b", "score": 0.9},
        ]
        overlay = [
            {"filename": "c.go", "location": "0:0", "code": "c", "score": 0.8},
        ]
        result = merge_results(main, overlay, [], top_k=10)
        scores = [r["score"] for r in result]
        assert scores == [0.9, 0.8, 0.7]

    def test_top_k_limits(self):
        main = [
            {"filename": f"f{i}.go", "location": "0:0", "code": f"c{i}", "score": 0.9 - i * 0.1}
            for i in range(5)
        ]
        result = merge_results(main, [], [], top_k=3)
        assert len(result) == 3

    def test_min_score_filters(self):
        main = [
            {"filename": "a.go", "location": "0:0", "code": "a", "score": 0.9},
            {"filename": "b.go", "location": "0:0", "code": "b", "score": 0.3},
            {"filename": "c.go", "location": "0:0", "code": "c", "score": 0.1},
        ]
        result = merge_results(main, [], [], top_k=10, min_score=0.5)
        assert len(result) == 1
        assert result[0]["filename"] == "a.go"

    def test_different_locations_not_deduped(self):
        """Same filename but different locations should both appear."""
        main = [
            {"filename": "a.go", "location": "0:0", "code": "chunk1", "score": 0.9},
            {"filename": "a.go", "location": "1:100", "code": "chunk2", "score": 0.8},
        ]
        overlay = [
            {"filename": "a.go", "location": "0:0", "code": "new chunk1", "score": 0.85},
        ]
        result = merge_results(main, overlay, [], top_k=10)
        assert len(result) == 2
        # overlay wins for location 0:0
        codes = {r["location"]: r["code"] for r in result}
        assert codes["0:0"] == "new chunk1"
        assert codes["1:100"] == "chunk2"

    def test_empty_inputs(self):
        result = merge_results([], [], [], top_k=10)
        assert result == []


# ===================================================================
# query_table
# ===================================================================


class TestQueryTable:
    def test_queries_correct_table(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchall.return_value = [
            ("a.go", "func main", "0:0", 0.92),
        ]
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        results = query_table("postgresql://x", "my_table", [0.1] * 384, top_k=5)

        assert len(results) == 1
        assert results[0]["filename"] == "a.go"
        assert results[0]["score"] == 0.92
        # Verify correct table in SQL
        sql = cursor.execute.call_args[0][0]
        assert "my_table" in sql

    def test_filters_by_min_score(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchall.return_value = [
            ("a.go", "good", "0:0", 0.9),
            ("b.go", "bad", "0:0", 0.1),
        ]
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        results = query_table("postgresql://x", "t", [0.1] * 384, min_score=0.5)
        assert len(results) == 1
        assert results[0]["filename"] == "a.go"

    def test_closes_connection(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchall.return_value = []
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        query_table("postgresql://x", "t", [0.1] * 384)
        conn.close.assert_called_once()


# ===================================================================
# get_deleted_files
# ===================================================================


class TestGetDeletedFiles:
    def test_returns_parsed_list(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchone.return_value = ('["old.go", "removed.go"]',)
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        result = get_deleted_files("postgresql://x", "eng-abc123")
        assert result == ["old.go", "removed.go"]

    def test_returns_empty_on_not_found(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchone.return_value = None
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        result = get_deleted_files("postgresql://x", "eng-abc123")
        assert result == []

    def test_returns_empty_on_null_value(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchone.return_value = (None,)
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        result = get_deleted_files("postgresql://x", "eng-abc123")
        assert result == []


# ===================================================================
# search
# ===================================================================


class TestSearch:
    def _make_config(self, **overrides):
        defaults = {
            "database_url": "postgresql://localhost/cocoindex",
            "engine_id": "eng-abc123",
            "main_table": "main_backend_embeddings",
            "overlay_table": "ovl_eng_abc123",
            "track": "backend",
            "worktree": "/path/to/worktree",
        }
        defaults.update(overrides)
        return ServerConfig(**defaults)

    @mock.patch("mcp_server.get_deleted_files", return_value=[])
    @mock.patch("mcp_server.query_table")
    @mock.patch("mcp_server.embed_query", return_value=[0.1] * 384)
    def test_dual_table_search(self, mock_embed, mock_query, mock_deleted):
        mock_query.side_effect = [
            [{"filename": "a.go", "location": "0:0", "code": "main", "score": 0.9}],
            [{"filename": "b.go", "location": "0:0", "code": "overlay", "score": 0.85}],
        ]
        cfg = self._make_config()
        results = search(cfg, "authentication handler")
        assert len(results) == 2
        assert mock_query.call_count == 2

    @mock.patch("mcp_server.query_table")
    @mock.patch("mcp_server.embed_query", return_value=[0.1] * 384)
    def test_main_only_when_no_overlay(self, mock_embed, mock_query):
        mock_query.return_value = [
            {"filename": "a.go", "location": "0:0", "code": "main", "score": 0.9},
        ]
        cfg = self._make_config(overlay_table=None)
        results = search(cfg, "query")
        assert len(results) == 1
        assert mock_query.call_count == 1

    @mock.patch("mcp_server.embed_query", return_value=[0.1] * 384)
    def test_empty_when_no_main_table(self, mock_embed):
        cfg = self._make_config(main_table=None)
        results = search(cfg, "query")
        assert results == []

    @mock.patch("mcp_server.query_table")
    @mock.patch("mcp_server.embed_query", return_value=[0.1] * 384)
    def test_multi_table_search_dispatcher(self, mock_embed, mock_query):
        """Dispatcher mode: search across multiple main tables."""
        mock_query.side_effect = [
            [{"filename": "a.go", "location": "0:0", "code": "backend", "score": 0.9}],
            [{"filename": "b.ts", "location": "0:0", "code": "frontend", "score": 0.85}],
        ]
        cfg = self._make_config(
            main_table="main_backend_embeddings",
            main_tables=["main_backend_embeddings", "main_frontend_embeddings"],
            overlay_table=None,
            engine_id=None,
        )
        results = search(cfg, "authentication")
        assert len(results) == 2
        assert mock_query.call_count == 2
        # Results should be sorted by score
        assert results[0]["score"] >= results[1]["score"]

    @mock.patch("mcp_server.query_table")
    @mock.patch("mcp_server.embed_query", return_value=[0.1] * 384)
    def test_multi_table_dedup_by_filename_location(self, mock_embed, mock_query):
        """Same file/location across tables should be deduped (highest score wins)."""
        mock_query.side_effect = [
            [{"filename": "shared.go", "location": "0:0", "code": "v1", "score": 0.7}],
            [{"filename": "shared.go", "location": "0:0", "code": "v2", "score": 0.9}],
        ]
        cfg = self._make_config(
            main_table="t1",
            main_tables=["t1", "t2"],
            overlay_table=None,
            engine_id=None,
        )
        results = search(cfg, "query")
        assert len(results) == 1
        assert results[0]["score"] == 0.9

    @mock.patch("mcp_server.query_table")
    @mock.patch("mcp_server.embed_query", return_value=[0.1] * 384)
    def test_multi_table_tolerates_missing_table(self, mock_embed, mock_query):
        """If one table doesn't exist, others still return results."""
        mock_query.side_effect = [
            Exception("relation does not exist"),
            [{"filename": "b.ts", "location": "0:0", "code": "ok", "score": 0.8}],
        ]
        cfg = self._make_config(
            main_table="t1",
            main_tables=["t1", "t2"],
            overlay_table=None,
            engine_id=None,
        )
        results = search(cfg, "query")
        assert len(results) == 1
        assert results[0]["filename"] == "b.ts"


# ===================================================================
# get_overlay_status
# ===================================================================


class TestGetOverlayStatus:
    def test_returns_not_found(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchone.return_value = None
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        cfg = ServerConfig(database_url="postgresql://x", engine_id="eng-1")
        result = get_overlay_status(cfg)
        assert result["status"] == "not_found"

    def test_returns_metadata(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchone.return_value = (
            "eng-1", "backend", "ry/test", "abc123",
            5, 42, '["old.go"]', "2026-02-19 12:00", "2026-02-19 12:05",
        )
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        cfg = ServerConfig(database_url="postgresql://x", engine_id="eng-1")
        result = get_overlay_status(cfg)
        assert result["status"] == "ok"
        assert result["files_indexed"] == 5
        assert result["chunks_indexed"] == 42

    def test_no_engine_id(self):
        cfg = ServerConfig(database_url="postgresql://x", engine_id=None)
        result = get_overlay_status(cfg)
        assert result["status"] == "no_engine_id"


# ===================================================================
# refresh_overlay
# ===================================================================


class TestRefreshOverlay:
    def setup_method(self):
        # Reset rate limiter between tests
        import mcp_server
        mcp_server._last_refresh_time = 0.0

    def test_missing_config_returns_error(self):
        cfg = ServerConfig(database_url="postgresql://x", engine_id=None)
        result = refresh_overlay(cfg)
        assert result["status"] == "error"

    @mock.patch("mcp_server.subprocess.run")
    def test_successful_refresh(self, mock_run):
        mock_run.return_value = mock.Mock(
            returncode=0,
            stdout='{"files_indexed": 3, "chunks_indexed": 15}',
            stderr="",
        )
        cfg = ServerConfig(
            database_url="postgresql://x",
            engine_id="eng-1",
            worktree="/wt",
            track="backend",
        )
        result = refresh_overlay(cfg)
        assert result["status"] == "ok"
        assert result["files_indexed"] == 3
        assert result["chunks_indexed"] == 15
        assert "duration_ms" in result

    @mock.patch("mcp_server.subprocess.run")
    def test_rate_limited(self, mock_run):
        import mcp_server
        mcp_server._last_refresh_time = time.time()  # just refreshed

        cfg = ServerConfig(
            database_url="postgresql://x",
            engine_id="eng-1",
            worktree="/wt",
            track="backend",
        )
        result = refresh_overlay(cfg)
        assert result["status"] == "rate_limited"
        assert "retry_after_sec" in result
        mock_run.assert_not_called()

    @mock.patch("mcp_server.subprocess.run")
    def test_subprocess_failure(self, mock_run):
        mock_run.return_value = mock.Mock(
            returncode=1,
            stdout="",
            stderr="git diff failed",
        )
        cfg = ServerConfig(
            database_url="postgresql://x",
            engine_id="eng-1",
            worktree="/wt",
            track="backend",
        )
        result = refresh_overlay(cfg)
        assert result["status"] == "error"
        assert "git diff failed" in result["message"]


# ===================================================================
# create_server
# ===================================================================


class TestCreateServer:
    def test_creates_server_with_tools(self):
        cfg = ServerConfig(
            database_url="postgresql://x",
            engine_id="eng-1",
            main_table="main_backend_embeddings",
        )
        server = create_server(cfg)
        assert "search_code" in server._tools
        assert "overlay_status" in server._tools
        assert "overlay_refresh" in server._tools

    def test_server_name(self):
        cfg = ServerConfig(database_url="postgresql://x")
        server = create_server(cfg)
        assert server.name == "railyard-cocoindex"
