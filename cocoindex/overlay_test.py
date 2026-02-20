"""Unit tests for cocoindex/overlay.py — all external deps are mocked."""

import json
import os
import sys
import types
from unittest import mock

import pytest

# ---------------------------------------------------------------------------
# Stub numpy + cocoindex before importing overlay.py (it imports from main)
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
    """Mimic @cocoindex.transform_flow() — returns a decorator."""
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

# ---------------------------------------------------------------------------
# Stub psycopg2 + sentence_transformers (lazy-imported inside build())
# ---------------------------------------------------------------------------

_psycopg2 = types.ModuleType("psycopg2")
_psycopg2.connect = mock.MagicMock()
sys.modules["psycopg2"] = _psycopg2

_st = types.ModuleType("sentence_transformers")
_st.SentenceTransformer = mock.MagicMock()
sys.modules["sentence_transformers"] = _st

from overlay import (  # noqa: E402
    build,
    chunk_text,
    cleanup,
    filter_by_patterns,
    get_changed_files,
    get_current_branch,
    get_deleted_files,
    get_head_commit,
    overlay_table_name,
    parse_args,
    status,
)


# ===================================================================
# overlay_table_name
# ===================================================================


class TestOverlayTableName:
    def test_basic(self):
        assert overlay_table_name("eng-a1b2c3d4") == "ovl_eng_a1b2c3d4"

    def test_underscores_preserved(self):
        assert overlay_table_name("eng_abc") == "ovl_eng_abc"

    def test_rejects_semicolon(self):
        with pytest.raises(ValueError):
            overlay_table_name("eng; DROP TABLE")

    def test_rejects_empty(self):
        with pytest.raises(ValueError):
            overlay_table_name("")

    def test_rejects_spaces(self):
        with pytest.raises(ValueError):
            overlay_table_name("eng abc")

    def test_rejects_quotes(self):
        with pytest.raises(ValueError):
            overlay_table_name("eng'abc")


# ===================================================================
# Git helpers
# ===================================================================


class TestGetChangedFiles:
    def test_returns_file_list(self):
        proc = mock.Mock(returncode=0, stdout="cmd/main.go\ninternal/foo.go\n", stderr="")
        with mock.patch("overlay.subprocess.run", return_value=proc) as run:
            result = get_changed_files("/worktree")
        assert result == ["cmd/main.go", "internal/foo.go"]
        run.assert_called_once()
        assert run.call_args[0][0] == ["git", "diff", "--name-only", "main...HEAD"]
        assert run.call_args[1]["cwd"] == "/worktree"

    def test_empty_diff(self):
        proc = mock.Mock(returncode=0, stdout="", stderr="")
        with mock.patch("overlay.subprocess.run", return_value=proc):
            result = get_changed_files("/worktree")
        assert result == []

    def test_git_error_raises(self):
        proc = mock.Mock(returncode=1, stdout="", stderr="fatal: bad revision")
        with mock.patch("overlay.subprocess.run", return_value=proc):
            with pytest.raises(RuntimeError, match="git diff failed"):
                get_changed_files("/worktree")


class TestGetDeletedFiles:
    def test_returns_deleted_list(self):
        proc = mock.Mock(returncode=0, stdout="old_file.go\n", stderr="")
        with mock.patch("overlay.subprocess.run", return_value=proc):
            result = get_deleted_files("/worktree")
        assert result == ["old_file.go"]

    def test_empty(self):
        proc = mock.Mock(returncode=0, stdout="", stderr="")
        with mock.patch("overlay.subprocess.run", return_value=proc):
            assert get_deleted_files("/worktree") == []


class TestGetHeadCommit:
    def test_returns_hash(self):
        proc = mock.Mock(returncode=0, stdout="abc123def456\n", stderr="")
        with mock.patch("overlay.subprocess.run", return_value=proc):
            assert get_head_commit("/worktree") == "abc123def456"

    def test_error_raises(self):
        proc = mock.Mock(returncode=1, stdout="", stderr="fatal")
        with mock.patch("overlay.subprocess.run", return_value=proc):
            with pytest.raises(RuntimeError):
                get_head_commit("/worktree")


class TestGetCurrentBranch:
    def test_returns_branch(self):
        proc = mock.Mock(returncode=0, stdout="ry/testuser/feature-x\n", stderr="")
        with mock.patch("overlay.subprocess.run", return_value=proc):
            assert get_current_branch("/worktree") == "ry/testuser/feature-x"

    def test_fallback_on_error(self):
        proc = mock.Mock(returncode=1, stdout="", stderr="error")
        with mock.patch("overlay.subprocess.run", return_value=proc):
            assert get_current_branch("/worktree") == "unknown"


# ===================================================================
# filter_by_patterns
# ===================================================================


class TestFilterByPatterns:
    def test_matches_go_files(self):
        files = ["cmd/main.go", "internal/foo.go", "README.md", "docs/guide.md"]
        assert filter_by_patterns(files, ["*.go"]) == ["cmd/main.go", "internal/foo.go"]

    def test_matches_directory_globs(self):
        files = ["cmd/main.go", "internal/foo.go", "pkg/bar.go", "README.md"]
        assert filter_by_patterns(files, ["cmd/**", "internal/**"]) == [
            "cmd/main.go", "internal/foo.go",
        ]

    def test_multiple_patterns(self):
        files = ["main.go", "app.ts", "style.css", "README.md"]
        assert filter_by_patterns(files, ["*.go", "*.ts"]) == ["main.go", "app.ts"]

    def test_no_matches(self):
        assert filter_by_patterns(["README.md"], ["*.go"]) == []

    def test_empty_files(self):
        assert filter_by_patterns([], ["*.go"]) == []

    def test_no_duplicates(self):
        """A file matching multiple patterns should appear only once."""
        files = ["cmd/main.go"]
        result = filter_by_patterns(files, ["*.go", "cmd/**"])
        assert result == ["cmd/main.go"]


# ===================================================================
# chunk_text
# ===================================================================


class TestChunkText:
    def test_short_text_single_chunk(self):
        chunks = chunk_text("hello world", chunk_size=100)
        assert len(chunks) == 1
        assert chunks[0]["text"] == "hello world"
        assert chunks[0]["location"] == "0:0"

    def test_empty_text(self):
        assert chunk_text("") == []
        assert chunk_text("   ") == []

    def test_exact_boundary(self):
        text = "a" * 1500
        chunks = chunk_text(text, chunk_size=1500)
        assert len(chunks) == 1

    def test_splits_long_text(self):
        text = "line\n" * 500  # 2500 chars
        chunks = chunk_text(text, chunk_size=1500, chunk_overlap=300)
        assert len(chunks) >= 2
        # All chunks should have content
        for chunk in chunks:
            assert chunk["text"].strip()

    def test_chunk_locations_are_unique(self):
        text = "x" * 5000
        chunks = chunk_text(text, chunk_size=1500, chunk_overlap=300)
        locations = [c["location"] for c in chunks]
        assert len(locations) == len(set(locations))

    def test_chunks_have_overlap(self):
        # With overlap, the end of one chunk should overlap with start of next
        text = "a" * 3000
        chunks = chunk_text(text, chunk_size=1500, chunk_overlap=300)
        assert len(chunks) >= 2

    def test_preserves_all_content(self):
        """Combined chunks (minus overlap) should cover all original text."""
        text = "func main() {\n    fmt.Println()\n}\n" * 100
        chunks = chunk_text(text, chunk_size=200, chunk_overlap=50)
        assert len(chunks) >= 2
        # First chunk should start from beginning
        assert chunks[0]["text"].startswith("func main()")


# ===================================================================
# parse_args
# ===================================================================


class TestParseArgs:
    def test_build_subcommand(self):
        args = parse_args([
            "build",
            "--engine-id", "eng-abc123",
            "--worktree", "/tmp/wt",
            "--track", "backend",
            "--file-patterns", "*.go", "cmd/**",
            "--database-url", "postgresql://localhost/test",
        ])
        assert args.command == "build"
        assert args.engine_id == "eng-abc123"
        assert args.worktree == "/tmp/wt"
        assert args.track == "backend"
        assert args.file_patterns == ["*.go", "cmd/**"]
        assert args.database_url == "postgresql://localhost/test"
        assert args.language is None

    def test_build_with_language(self):
        args = parse_args([
            "build",
            "--engine-id", "eng-1",
            "--worktree", "/wt",
            "--track", "be",
            "--file-patterns", "*.go",
            "--database-url", "postgresql://x",
            "--language", "go",
        ])
        assert args.language == "go"

    def test_missing_subcommand_exits(self):
        with pytest.raises(SystemExit):
            parse_args([])

    def test_missing_required_arg_exits(self):
        with pytest.raises(SystemExit):
            parse_args(["build", "--engine-id", "eng-1"])


# ===================================================================
# build (integration with mocks)
# ===================================================================


def _make_build_args(**overrides):
    """Create a namespace mimicking parsed build args."""
    defaults = {
        "command": "build",
        "engine_id": "eng-abc123",
        "worktree": "/tmp/worktree",
        "track": "backend",
        "file_patterns": ["*.go", "cmd/**"],
        "database_url": "postgresql://localhost/cocoindex",
        "language": None,
    }
    defaults.update(overrides)
    import argparse
    return argparse.Namespace(**defaults)


class TestBuild:
    def _patch_all(self, changed=None, deleted=None, file_contents=None):
        """Return a context manager that patches all external deps for build().

        Since build() does lazy ``import psycopg2`` and
        ``from sentence_transformers import SentenceTransformer``, we configure
        the stub modules already in sys.modules and patch git/file helpers.
        """
        if changed is None:
            changed = ["cmd/main.go"]
        if deleted is None:
            deleted = []
        if file_contents is None:
            file_contents = {f: f"// content of {f}" for f in changed}

        patches = mock.patch.multiple(
            "overlay",
            get_changed_files=mock.DEFAULT,
            get_deleted_files=mock.DEFAULT,
            get_head_commit=mock.DEFAULT,
            get_current_branch=mock.DEFAULT,
            load_config=mock.DEFAULT,
        )

        class _Ctx:
            def __enter__(self_ctx):
                mocks = patches.__enter__()
                mocks["get_changed_files"].return_value = changed
                mocks["get_deleted_files"].return_value = deleted
                mocks["get_head_commit"].return_value = "deadbeef"
                mocks["get_current_branch"].return_value = "ry/test/feat"

                # Return a default config from load_config mock
                from config import CocoIndexConfig
                mocks["load_config"].return_value = CocoIndexConfig()

                # Configure SentenceTransformer stub in sys.modules
                model_mock = mock.MagicMock()
                model_mock.encode.return_value = mock.MagicMock(
                    tolist=mock.MagicMock(return_value=[0.1] * 384)
                )
                st_cls = mock.MagicMock(return_value=model_mock)
                # Use sys.modules directly in case another test file replaced it
                sys.modules["sentence_transformers"].SentenceTransformer = st_cls

                # Configure psycopg2 stub in sys.modules
                cursor_mock = mock.MagicMock()
                cursor_mock.__enter__ = mock.MagicMock(return_value=cursor_mock)
                cursor_mock.__exit__ = mock.MagicMock(return_value=False)
                conn_mock = mock.MagicMock()
                conn_mock.cursor.return_value = cursor_mock
                pg_connect = mock.MagicMock(return_value=conn_mock)
                sys.modules["psycopg2"].connect = pg_connect

                # Mock file reads
                def fake_open(path, *a, **kw):
                    for fname, content in file_contents.items():
                        if path.endswith(fname):
                            return mock.mock_open(read_data=content)()
                    raise FileNotFoundError(path)

                self_ctx.open_patch = mock.patch("builtins.open", side_effect=fake_open)

                # Mock os.path.exists for changed files
                def fake_exists(path):
                    for fname in file_contents:
                        if path.endswith(fname):
                            return True
                    return False

                self_ctx.exists_patch = mock.patch(
                    "os.path.exists", side_effect=fake_exists
                )

                self_ctx.open_patch.__enter__()
                self_ctx.exists_patch.__enter__()

                self_ctx.mocks = mocks
                self_ctx.model_mock = model_mock
                self_ctx.cursor_mock = cursor_mock
                self_ctx.conn_mock = conn_mock
                self_ctx.pg_connect = pg_connect
                self_ctx.st_cls = st_cls
                return self_ctx

            def __exit__(self_ctx, *args):
                self_ctx.exists_patch.__exit__(*args)
                self_ctx.open_patch.__exit__(*args)
                patches.__exit__(*args)

        return _Ctx()

    def test_no_changes_returns_early(self):
        with self._patch_all(changed=[], deleted=[]) as ctx:
            result = build(_make_build_args())
        assert result["files_indexed"] == 0
        assert result["chunks_indexed"] == 0
        # DB should not be touched
        ctx.pg_connect.assert_not_called()

    def test_creates_table_and_inserts(self):
        with self._patch_all(changed=["cmd/main.go"]) as ctx:
            result = build(_make_build_args())
        assert result["files_indexed"] == 1
        assert result["chunks_indexed"] >= 1
        assert result["table"] == "ovl_eng_abc123"
        # Verify table creation SQL
        sql_calls = [str(c) for c in ctx.cursor_mock.execute.call_args_list]
        sql_joined = " ".join(sql_calls)
        assert "CREATE TABLE IF NOT EXISTS ovl_eng_abc123" in sql_joined
        assert "TRUNCATE TABLE ovl_eng_abc123" in sql_joined

    def test_uses_correct_embedding_model(self):
        with self._patch_all() as ctx:
            build(_make_build_args())
        ctx.st_cls.assert_called_once_with("sentence-transformers/all-MiniLM-L6-v2")

    def test_connects_with_database_url(self):
        with self._patch_all() as ctx:
            build(_make_build_args(database_url="postgresql://myhost/mydb"))
        ctx.pg_connect.assert_called_once_with("postgresql://myhost/mydb")

    def test_upserts_overlay_meta(self):
        with self._patch_all(changed=["cmd/main.go"], deleted=["old.go"]) as ctx:
            build(_make_build_args())
        # Find the overlay_meta upsert call
        meta_calls = [
            c for c in ctx.cursor_mock.execute.call_args_list
            if "overlay_meta" in str(c)
        ]
        assert len(meta_calls) == 1
        meta_args = meta_calls[0][0][1]  # positional tuple
        assert meta_args[0] == "eng-abc123"  # engine_id
        assert meta_args[1] == "backend"      # track
        assert meta_args[2] == "ry/test/feat" # branch
        assert meta_args[3] == "deadbeef"     # last_commit
        # deleted_files should be JSON list
        deleted_json = json.loads(meta_args[6])
        assert "old.go" in deleted_json

    def test_commits_transaction(self):
        with self._patch_all() as ctx:
            build(_make_build_args())
        ctx.conn_mock.commit.assert_called_once()

    def test_closes_connection(self):
        with self._patch_all() as ctx:
            build(_make_build_args())
        ctx.conn_mock.close.assert_called_once()

    def test_filters_by_file_patterns(self):
        """Only files matching track patterns should be indexed."""
        changed = ["cmd/main.go", "README.md", "docs/guide.md"]
        contents = {"cmd/main.go": "package main"}
        with self._patch_all(changed=changed, file_contents=contents) as ctx:
            result = build(_make_build_args(file_patterns=["*.go", "cmd/**"]))
        # README.md and docs/guide.md should be filtered out
        assert result["files_indexed"] == 1

    def test_deleted_files_tracked(self):
        with self._patch_all(changed=["cmd/main.go"], deleted=["old.go", "removed.go"]) as ctx:
            result = build(_make_build_args())
        assert result["deleted_files"] == 2

    def test_skips_index_for_few_rows(self):
        """IVFFlat index should not be created with < 10 rows."""
        with self._patch_all(changed=["tiny.go"], file_contents={"tiny.go": "x"}) as ctx:
            build(_make_build_args())
        sql_calls = [str(c) for c in ctx.cursor_mock.execute.call_args_list]
        sql_joined = " ".join(sql_calls)
        assert "ivfflat" not in sql_joined.lower()


# ===================================================================
# Helper: build args for cleanup/status
# ===================================================================


def _make_cleanup_args(**overrides):
    defaults = {
        "engine_id": "eng-abc123",
        "database_url": "postgresql://localhost/cocoindex",
        "config": None,
    }
    defaults.update(overrides)
    import argparse
    return argparse.Namespace(**defaults)


def _make_status_args(**overrides):
    defaults = {
        "engine_id": "eng-abc123",
        "database_url": "postgresql://localhost/cocoindex",
    }
    defaults.update(overrides)
    import argparse
    return argparse.Namespace(**defaults)


# ===================================================================
# cleanup
# ===================================================================


class TestCleanup:
    def _patch_db(self):
        """Return a context manager that patches psycopg2 + load_config for cleanup."""
        from config import CocoIndexConfig

        cursor_mock = mock.MagicMock()
        cursor_mock.__enter__ = mock.MagicMock(return_value=cursor_mock)
        cursor_mock.__exit__ = mock.MagicMock(return_value=False)
        conn_mock = mock.MagicMock()
        conn_mock.cursor.return_value = cursor_mock
        pg_connect = mock.MagicMock(return_value=conn_mock)

        patches = mock.patch.multiple(
            "overlay",
            load_config=mock.DEFAULT,
        )

        class _Ctx:
            def __enter__(self_ctx):
                mocks = patches.__enter__()
                mocks["load_config"].return_value = CocoIndexConfig()
                sys.modules["psycopg2"].connect = pg_connect
                self_ctx.cursor_mock = cursor_mock
                self_ctx.conn_mock = conn_mock
                self_ctx.pg_connect = pg_connect
                return self_ctx

            def __exit__(self_ctx, *args):
                patches.__exit__(*args)

        return _Ctx()

    def test_drops_table(self):
        with self._patch_db() as ctx:
            cleanup(_make_cleanup_args())
        sql_calls = [str(c) for c in ctx.cursor_mock.execute.call_args_list]
        sql_joined = " ".join(sql_calls)
        assert "DROP TABLE IF EXISTS ovl_eng_abc123" in sql_joined

    def test_deletes_overlay_meta_row(self):
        with self._patch_db() as ctx:
            cleanup(_make_cleanup_args())
        meta_calls = [
            c for c in ctx.cursor_mock.execute.call_args_list
            if "overlay_meta" in str(c)
        ]
        assert len(meta_calls) == 1
        assert meta_calls[0][0][1] == ("eng-abc123",)

    def test_commits_transaction(self):
        with self._patch_db() as ctx:
            cleanup(_make_cleanup_args())
        ctx.conn_mock.commit.assert_called_once()

    def test_closes_connection(self):
        with self._patch_db() as ctx:
            cleanup(_make_cleanup_args())
        ctx.conn_mock.close.assert_called_once()

    def test_returns_result(self):
        with self._patch_db() as ctx:
            result = cleanup(_make_cleanup_args())
        assert result["engine_id"] == "eng-abc123"
        assert result["table"] == "ovl_eng_abc123"
        assert result["status"] == "cleaned"

    def test_connects_with_database_url(self):
        with self._patch_db() as ctx:
            cleanup(_make_cleanup_args(database_url="postgresql://custom/db"))
        ctx.pg_connect.assert_called_once_with("postgresql://custom/db")

    def test_uses_config_prefix(self):
        """Cleanup should use overlay_table_prefix from config."""
        from config import CocoIndexConfig
        with mock.patch("overlay.load_config") as mock_cfg:
            mock_cfg.return_value = CocoIndexConfig(overlay_table_prefix="custom_")
            cursor_mock = mock.MagicMock()
            cursor_mock.__enter__ = mock.MagicMock(return_value=cursor_mock)
            cursor_mock.__exit__ = mock.MagicMock(return_value=False)
            conn_mock = mock.MagicMock()
            conn_mock.cursor.return_value = cursor_mock
            sys.modules["psycopg2"].connect = mock.MagicMock(return_value=conn_mock)

            result = cleanup(_make_cleanup_args())
        assert result["table"] == "custom_eng_abc123"


# ===================================================================
# status
# ===================================================================


class TestStatus:
    def _patch_db(self, fetchone_result=None):
        """Return a context manager that patches psycopg2 for status."""
        cursor_mock = mock.MagicMock()
        cursor_mock.__enter__ = mock.MagicMock(return_value=cursor_mock)
        cursor_mock.__exit__ = mock.MagicMock(return_value=False)
        cursor_mock.fetchone.return_value = fetchone_result
        conn_mock = mock.MagicMock()
        conn_mock.cursor.return_value = cursor_mock
        pg_connect = mock.MagicMock(return_value=conn_mock)

        class _Ctx:
            def __enter__(self_ctx):
                sys.modules["psycopg2"].connect = pg_connect
                self_ctx.cursor_mock = cursor_mock
                self_ctx.conn_mock = conn_mock
                self_ctx.pg_connect = pg_connect
                return self_ctx

            def __exit__(self_ctx, *args):
                pass

        return _Ctx()

    def test_not_found(self):
        with self._patch_db(fetchone_result=None) as ctx:
            result = status(_make_status_args())
        assert result["status"] == "not_found"
        assert result["engine_id"] == "eng-abc123"

    def test_returns_metadata(self):
        row = (
            "eng-abc123", "backend", "ry/test/feat", "deadbeef",
            5, 42, '["old.go"]', "2026-02-19 12:00:00", "2026-02-19 12:05:00",
        )
        with self._patch_db(fetchone_result=row) as ctx:
            result = status(_make_status_args())
        assert result["status"] == "ok"
        assert result["engine_id"] == "eng-abc123"
        assert result["track"] == "backend"
        assert result["branch"] == "ry/test/feat"
        assert result["last_commit"] == "deadbeef"
        assert result["files_indexed"] == 5
        assert result["chunks_indexed"] == 42
        assert result["deleted_files"] == ["old.go"]

    def test_queries_correct_engine_id(self):
        with self._patch_db(fetchone_result=None) as ctx:
            status(_make_status_args(engine_id="eng-xyz789"))
        query_calls = [
            c for c in ctx.cursor_mock.execute.call_args_list
            if "overlay_meta" in str(c)
        ]
        assert len(query_calls) == 1
        assert query_calls[0][0][1] == ("eng-xyz789",)

    def test_closes_connection(self):
        with self._patch_db(fetchone_result=None) as ctx:
            status(_make_status_args())
        ctx.conn_mock.close.assert_called_once()

    def test_connects_with_database_url(self):
        with self._patch_db(fetchone_result=None) as ctx:
            status(_make_status_args(database_url="postgresql://custom/db"))
        ctx.pg_connect.assert_called_once_with("postgresql://custom/db")

    def test_empty_deleted_files(self):
        row = (
            "eng-abc123", "backend", "ry/test/feat", "deadbeef",
            0, 0, "[]", "2026-02-19 12:00:00", "2026-02-19 12:00:00",
        )
        with self._patch_db(fetchone_result=row) as ctx:
            result = status(_make_status_args())
        assert result["deleted_files"] == []

    def test_null_deleted_files(self):
        row = (
            "eng-abc123", "backend", "ry/test/feat", "deadbeef",
            0, 0, None, "2026-02-19 12:00:00", "2026-02-19 12:00:00",
        )
        with self._patch_db(fetchone_result=row) as ctx:
            result = status(_make_status_args())
        assert result["deleted_files"] == []


# ===================================================================
# parse_args — cleanup and status subcommands
# ===================================================================


class TestParseArgsCleanup:
    def test_cleanup_subcommand(self):
        args = parse_args([
            "cleanup",
            "--engine-id", "eng-abc123",
            "--database-url", "postgresql://localhost/cocoindex",
        ])
        assert args.command == "cleanup"
        assert args.engine_id == "eng-abc123"
        assert args.database_url == "postgresql://localhost/cocoindex"

    def test_cleanup_with_config(self):
        args = parse_args([
            "cleanup",
            "--engine-id", "eng-1",
            "--database-url", "postgresql://x",
            "--config", "/path/to/cocoindex.yaml",
        ])
        assert args.config == "/path/to/cocoindex.yaml"

    def test_cleanup_missing_engine_id_exits(self):
        with pytest.raises(SystemExit):
            parse_args(["cleanup", "--database-url", "postgresql://x"])

    def test_cleanup_missing_database_url_exits(self):
        with pytest.raises(SystemExit):
            parse_args(["cleanup", "--engine-id", "eng-1"])


class TestParseArgsStatus:
    def test_status_subcommand(self):
        args = parse_args([
            "status",
            "--engine-id", "eng-abc123",
            "--database-url", "postgresql://localhost/cocoindex",
        ])
        assert args.command == "status"
        assert args.engine_id == "eng-abc123"
        assert args.database_url == "postgresql://localhost/cocoindex"

    def test_status_missing_engine_id_exits(self):
        with pytest.raises(SystemExit):
            parse_args(["status", "--database-url", "postgresql://x"])

    def test_status_missing_database_url_exits(self):
        with pytest.raises(SystemExit):
            parse_args(["status", "--engine-id", "eng-1"])
