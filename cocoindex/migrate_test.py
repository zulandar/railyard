"""Unit tests for cocoindex/migrate.py."""

import os
import sys
import types
from unittest import mock

import pytest

# ---------------------------------------------------------------------------
# Stub psycopg2 before importing migrate.py
# ---------------------------------------------------------------------------

_psycopg2 = types.ModuleType("psycopg2")
_psycopg2.connect = mock.MagicMock()
sys.modules["psycopg2"] = _psycopg2

from migrate import (  # noqa: E402
    apply_migration,
    ensure_migrations_table,
    get_applied_migrations,
    get_migration_files,
    parse_args,
    run_migrations,
    MIGRATIONS_DIR,
)


# ===================================================================
# get_migration_files
# ===================================================================


class TestGetMigrationFiles:
    def test_finds_sql_files(self):
        files = get_migration_files()
        names = [name for name, _ in files]
        assert "001_create_overlay_meta.sql" in names

    def test_sorted_order(self):
        files = get_migration_files()
        names = [name for name, _ in files]
        assert names == sorted(names)

    def test_paths_exist(self):
        for name, path in get_migration_files():
            assert os.path.exists(path), f"{path} does not exist"


# ===================================================================
# ensure_migrations_table
# ===================================================================


class TestEnsureMigrationsTable:
    def test_creates_table(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor

        ensure_migrations_table(conn)

        sql = cursor.execute.call_args[0][0]
        assert "CREATE TABLE IF NOT EXISTS _migrations" in sql
        conn.commit.assert_called_once()


# ===================================================================
# get_applied_migrations
# ===================================================================


class TestGetAppliedMigrations:
    def test_returns_set_of_names(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchall.return_value = [("001_foo.sql",), ("002_bar.sql",)]
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor

        result = get_applied_migrations(conn)
        assert result == {"001_foo.sql", "002_bar.sql"}

    def test_empty_table(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchall.return_value = []
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor

        assert get_applied_migrations(conn) == set()


# ===================================================================
# apply_migration
# ===================================================================


class TestApplyMigration:
    def test_executes_sql_and_records(self, tmp_path):
        sql_file = tmp_path / "001_test.sql"
        sql_file.write_text("CREATE TABLE foo (id INT);")

        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        conn = mock.MagicMock()
        conn.cursor.return_value = cursor

        apply_migration(conn, "001_test.sql", str(sql_file))

        # Should execute the SQL content
        calls = cursor.execute.call_args_list
        assert "CREATE TABLE foo (id INT);" in calls[0][0][0]
        # Should record the migration
        assert calls[1][0][0] == "INSERT INTO _migrations (name) VALUES (%s)"
        assert calls[1][0][1] == ("001_test.sql",)
        conn.commit.assert_called_once()


# ===================================================================
# run_migrations
# ===================================================================


class TestRunMigrations:
    def test_applies_pending_migrations(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchall.return_value = []  # no applied migrations yet

        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        result = run_migrations("postgresql://localhost/test")

        assert "001_create_overlay_meta.sql" in result
        _psycopg2.connect.assert_called_once_with("postgresql://localhost/test")
        conn.close.assert_called_once()

    def test_skips_already_applied(self):
        cursor = mock.MagicMock()
        cursor.__enter__ = mock.MagicMock(return_value=cursor)
        cursor.__exit__ = mock.MagicMock(return_value=False)
        cursor.fetchall.return_value = [("001_create_overlay_meta.sql",)]

        conn = mock.MagicMock()
        conn.cursor.return_value = cursor
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        result = run_migrations("postgresql://localhost/test")

        assert result == []

    def test_closes_connection_on_error(self):
        conn = mock.MagicMock()
        conn.cursor.side_effect = RuntimeError("db error")
        _psycopg2.connect = mock.MagicMock(return_value=conn)

        with pytest.raises(RuntimeError, match="db error"):
            run_migrations("postgresql://localhost/test")

        conn.close.assert_called_once()


# ===================================================================
# parse_args
# ===================================================================


class TestParseArgs:
    def test_database_url(self):
        args = parse_args(["--database-url", "postgresql://localhost/cocoindex"])
        assert args.database_url == "postgresql://localhost/cocoindex"

    def test_missing_url_exits(self):
        with pytest.raises(SystemExit):
            parse_args([])


# ===================================================================
# SQL content validation
# ===================================================================


class TestMigrationSQL:
    def test_001_contains_overlay_meta(self):
        """The SQL file should create the overlay_meta table."""
        sql_path = os.path.join(MIGRATIONS_DIR, "001_create_overlay_meta.sql")
        with open(sql_path) as f:
            sql = f.read()
        assert "CREATE TABLE IF NOT EXISTS overlay_meta" in sql
        assert "engine_id" in sql
        assert "track" in sql
        assert "branch" in sql
        assert "last_commit" in sql
        assert "files_indexed" in sql
        assert "chunks_indexed" in sql
        assert "deleted_files" in sql
        assert "created_at" in sql
        assert "updated_at" in sql

    def test_001_enables_pgvector(self):
        sql_path = os.path.join(MIGRATIONS_DIR, "001_create_overlay_meta.sql")
        with open(sql_path) as f:
            sql = f.read()
        assert "CREATE EXTENSION IF NOT EXISTS vector" in sql

    def test_001_engine_id_is_primary_key(self):
        sql_path = os.path.join(MIGRATIONS_DIR, "001_create_overlay_meta.sql")
        with open(sql_path) as f:
            sql = f.read()
        assert "PRIMARY KEY" in sql
