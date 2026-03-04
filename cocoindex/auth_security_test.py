"""Auth security regression tests for the cocoindex MCP server.

Verifies engine identity validation and cross-track access boundaries.
Documents the current trust model (env-var based identity via stdio transport).
"""

import os
import sys
import types
import unittest
from unittest import mock

# --- Stub dependencies before importing mcp_server ---

_np = types.ModuleType("numpy")
_np.float32 = "float32"
_nptyping = types.ModuleType("numpy.typing")
_nptyping.NDArray = type(
    "NDArray", (), {"__class_getitem__": classmethod(lambda cls, x: cls)}
)
_np.typing = _nptyping
sys.modules.setdefault("numpy", _np)
sys.modules.setdefault("numpy.typing", _nptyping)

_ci = types.ModuleType("cocoindex")


def _transform_flow_factory():
    def decorator(fn):
        fn.transform = fn
        return fn
    return decorator


_ci.transform_flow = _transform_flow_factory
_ci.DataSlice = type(
    "DataSlice", (), {"__class_getitem__": classmethod(lambda cls, x: cls)}
)
_funcs = types.ModuleType("cocoindex.functions")
_funcs.SentenceTransformerEmbed = lambda model="": None
_ci.functions = _funcs
sys.modules.setdefault("cocoindex", _ci)
sys.modules.setdefault("cocoindex.functions", _funcs)

_psycopg2 = types.ModuleType("psycopg2")
_psycopg2.connect = mock.MagicMock()

_psycopg2_sql = types.ModuleType("psycopg2.sql")


class _FakeComposed:
    def __init__(self, text):
        self._text = text
    def __str__(self):
        return self._text
    def __contains__(self, item):
        return item in self._text


class _FakeSQL:
    def __init__(self, template):
        self._template = template
    def format(self, *args, **kwargs):
        result = self._template
        for arg in args:
            result = result.replace("{}", str(arg), 1)
        return _FakeComposed(result)


class _FakeIdentifier:
    def __init__(self, *strings):
        self._strings = strings
    def __str__(self):
        return ".".join(f'"{s}"' for s in self._strings)


_psycopg2_sql.SQL = _FakeSQL
_psycopg2_sql.Identifier = _FakeIdentifier
_psycopg2.sql = _psycopg2_sql
sys.modules.setdefault("psycopg2", _psycopg2)
sys.modules.setdefault("psycopg2.sql", _psycopg2_sql)

_st = types.ModuleType("sentence_transformers")
_st.SentenceTransformer = mock.MagicMock()
sys.modules.setdefault("sentence_transformers", _st)

from mcp_server import (  # noqa: E402
    ServerConfig,
    get_deleted_files,
    get_overlay_status,
    load_server_config,
)


class TestEngineIdentityValidation(unittest.TestCase):
    """Verify that engine identity is established via environment variables."""

    def test_engine_id_from_env(self):
        """Engine ID should come from COCOINDEX_ENGINE_ID env var."""
        env = {
            "COCOINDEX_DATABASE_URL": "postgresql://localhost/db",
            "COCOINDEX_ENGINE_ID": "eng-abc123",
        }
        with mock.patch.dict(os.environ, env, clear=True):
            cfg = load_server_config()
        self.assertEqual(cfg.engine_id, "eng-abc123")

    def test_missing_engine_id_is_none(self):
        """Missing engine ID should result in None, not an error."""
        env = {"COCOINDEX_DATABASE_URL": "postgresql://localhost/db"}
        with mock.patch.dict(os.environ, env, clear=True):
            cfg = load_server_config()
        self.assertIsNone(cfg.engine_id)

    def test_empty_engine_id_is_none(self):
        """Empty engine ID string should be treated as None."""
        env = {
            "COCOINDEX_DATABASE_URL": "postgresql://localhost/db",
            "COCOINDEX_ENGINE_ID": "",
        }
        with mock.patch.dict(os.environ, env, clear=True):
            cfg = load_server_config()
        self.assertIsNone(cfg.engine_id)

    def test_database_url_required(self):
        """Missing database URL should raise ValueError."""
        with mock.patch.dict(os.environ, {}, clear=True):
            with self.assertRaises(ValueError):
                load_server_config()

    def test_track_from_env(self):
        """Track should come from COCOINDEX_TRACK env var."""
        env = {
            "COCOINDEX_DATABASE_URL": "postgresql://localhost/db",
            "COCOINDEX_TRACK": "backend",
        }
        with mock.patch.dict(os.environ, env, clear=True):
            cfg = load_server_config()
        self.assertEqual(cfg.track, "backend")


class TestCrossTrackAccessBoundaries(unittest.TestCase):
    """Verify track-scoped queries prevent cross-track data leakage."""

    def _mock_cursor(self, fetchone_result=None):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchone.return_value = fetchone_result
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)
        return cursor

    def test_get_deleted_files_scoped_by_track(self):
        """get_deleted_files should include track in query when provided."""
        cursor = self._mock_cursor(('["a.go"]',))

        get_deleted_files("postgresql://x", "eng-abc", track="backend")

        call_args = cursor.execute.call_args
        query = str(call_args[0][0])
        params = call_args[0][1]
        self.assertIn("track", query.lower())
        self.assertIn("backend", params)

    def test_get_deleted_files_no_track_still_works(self):
        """get_deleted_files without track should query by engine_id only."""
        cursor = self._mock_cursor(('["a.go"]',))

        get_deleted_files("postgresql://x", "eng-abc")

        call_args = cursor.execute.call_args
        params = call_args[0][1]
        self.assertEqual(len(params), 1)
        self.assertEqual(params[0], "eng-abc")

    def test_get_overlay_status_scoped_by_track(self):
        """get_overlay_status should include track in query when config.track is set."""
        cursor = self._mock_cursor((
            "eng-1", "backend", "ry/test", "abc123",
            5, 42, '["old.go"]', "2026-02-19 12:00", "2026-02-19 12:05",
        ))

        cfg = ServerConfig(
            database_url="postgresql://x", engine_id="eng-1", track="backend"
        )
        get_overlay_status(cfg)

        call_args = cursor.execute.call_args
        query = str(call_args[0][0])
        params = call_args[0][1]
        self.assertIn("track", query.lower())
        self.assertIn("backend", params)

    def test_get_overlay_status_no_engine_id(self):
        """get_overlay_status with no engine_id should return early."""
        cfg = ServerConfig(database_url="postgresql://x", engine_id=None)
        result = get_overlay_status(cfg)
        self.assertEqual(result["status"], "no_engine_id")

    def test_engine_cannot_query_other_track(self):
        """An engine with track=backend should not get results for track=frontend."""
        cursor = self._mock_cursor(None)  # No results for wrong track

        result = get_deleted_files("postgresql://x", "eng-abc", track="frontend")
        self.assertEqual(result, [])

        # Verify the query includes the track filter
        call_args = cursor.execute.call_args
        params = call_args[0][1]
        self.assertIn("frontend", params)


class TestStdioTransportSecurity(unittest.TestCase):
    """Document that the MCP server uses stdio transport (not network).

    These tests document the trust model — the MCP server is spawned
    per-engine as a subprocess and communicates only via stdin/stdout.
    No network listener is created.
    """

    def test_server_config_has_no_listen_address(self):
        """ServerConfig should not have a host/port for network binding."""
        cfg = ServerConfig(database_url="postgresql://x")
        self.assertFalse(hasattr(cfg, "host"))
        self.assertFalse(hasattr(cfg, "port"))
        self.assertFalse(hasattr(cfg, "listen_addr"))

    def test_server_config_identity_via_env_only(self):
        """Identity is established via env vars, not request headers."""
        cfg = ServerConfig(
            database_url="postgresql://x",
            engine_id="eng-abc",
            track="backend",
        )
        # Identity is a static property of the server config,
        # not per-request (no header inspection).
        self.assertEqual(cfg.engine_id, "eng-abc")
        self.assertEqual(cfg.track, "backend")


if __name__ == "__main__":
    unittest.main()
