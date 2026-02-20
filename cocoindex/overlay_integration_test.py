"""
Integration test for cocoindex/overlay.py — requires running pgvector and
sentence-transformers installed.

Run:
    cd cocoindex
    pytest overlay_integration_test.py -v

Prerequisites:
    - pgvector container running (docker compose -f docker/docker-compose.pgvector.yaml up -d)
    - Python deps: pip install psycopg2-binary sentence-transformers pytest
    - git available on PATH

Skip behavior:
    - Skips all tests if psycopg2 cannot connect to pgvector
    - Skips build test if sentence_transformers is not installed
"""

import json
import os
import shutil
import subprocess
import tempfile

import pytest

# ---------------------------------------------------------------------------
# Dependency checks — skip entire module if pgvector is not reachable
# ---------------------------------------------------------------------------

DATABASE_URL = os.environ.get(
    "COCOINDEX_TEST_DATABASE_URL",
    "postgresql://cocoindex:cocoindex@localhost:5481/cocoindex",
)

try:
    import psycopg2

    _conn = psycopg2.connect(DATABASE_URL)
    _conn.close()
    _pgvector_available = True
except Exception:
    _pgvector_available = False

pytestmark = pytest.mark.skipif(
    not _pgvector_available,
    reason="pgvector not available (start with: docker compose -f docker/docker-compose.pgvector.yaml up -d)",
)

try:
    import sentence_transformers  # noqa: F401
    _st_available = True
except ImportError:
    _st_available = False


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

ENGINE_ID = "eng-integtest01"
TRACK = "backend"
TABLE_NAME = f"ovl_eng_integtest01"


def _run_overlay(args: list[str], cwd: str | None = None) -> dict:
    """Run overlay.py as a subprocess and return parsed JSON output."""
    script = os.path.join(os.path.dirname(__file__), "overlay.py")
    python = shutil.which("python3") or "python3"

    # Use venv python if available
    venv_python = os.path.join(os.path.dirname(__file__), ".venv", "bin", "python")
    if os.path.exists(venv_python):
        python = venv_python

    cmd = [python, script] + args
    result = subprocess.run(
        cmd, capture_output=True, text=True, cwd=cwd, timeout=120,
    )
    if result.returncode != 0:
        raise RuntimeError(
            f"overlay.py failed (exit {result.returncode}):\n"
            f"stdout: {result.stdout}\nstderr: {result.stderr}"
        )
    # Parse the last line of stdout as JSON (overlay.py prints JSON result)
    lines = result.stdout.strip().splitlines()
    if not lines:
        raise RuntimeError("overlay.py produced no output")
    return json.loads(lines[-1])


def _query_db(sql: str, params: tuple = ()) -> list:
    """Execute a query against pgvector and return all rows."""
    conn = psycopg2.connect(DATABASE_URL)
    try:
        with conn.cursor() as cur:
            cur.execute(sql, params)
            return cur.fetchall()
    finally:
        conn.close()


def _table_exists(table: str) -> bool:
    """Check if a table exists in pgvector."""
    rows = _query_db(
        "SELECT 1 FROM information_schema.tables WHERE table_name = %s",
        (table,),
    )
    return len(rows) > 0


def _overlay_meta_row(engine_id: str) -> dict | None:
    """Fetch the overlay_meta row for an engine, or None."""
    rows = _query_db(
        "SELECT engine_id, track, branch, last_commit, "
        "files_indexed, chunks_indexed, deleted_files "
        "FROM overlay_meta WHERE engine_id = %s",
        (engine_id,),
    )
    if not rows:
        return None
    r = rows[0]
    return {
        "engine_id": r[0],
        "track": r[1],
        "branch": r[2],
        "last_commit": r[3],
        "files_indexed": r[4],
        "chunks_indexed": r[5],
        "deleted_files": json.loads(r[6]) if r[6] else [],
    }


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def git_repo(tmp_path):
    """Create a git repo with main branch and a feature branch with changes.

    Repo structure on main:
        hello.go        — a Go file
        README.md       — a markdown file

    Feature branch (feature/overlay-test) adds:
        handler.go      — new Go file
        (modifies hello.go)
        (deletes README.md)
    """
    repo = str(tmp_path / "repo")
    os.makedirs(repo)

    def git(*args):
        result = subprocess.run(
            ["git"] + list(args), cwd=repo,
            capture_output=True, text=True,
        )
        if result.returncode != 0:
            raise RuntimeError(f"git {' '.join(args)} failed: {result.stderr}")
        return result.stdout.strip()

    # Init repo with main branch
    git("init", "-b", "main")
    git("config", "user.name", "Test")
    git("config", "user.email", "test@test.com")

    # Create initial files on main
    with open(os.path.join(repo, "hello.go"), "w") as f:
        f.write('package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hello")\n}\n')
    with open(os.path.join(repo, "README.md"), "w") as f:
        f.write("# Test Repo\n\nThis is a test repository.\n")

    git("add", ".")
    git("commit", "-m", "Initial commit")

    # Create feature branch with changes
    git("checkout", "-b", "feature/overlay-test")

    # Modify hello.go
    with open(os.path.join(repo, "hello.go"), "w") as f:
        f.write(
            'package main\n\nimport "fmt"\n\n'
            "func main() {\n"
            '\tfmt.Println("hello from overlay")\n'
            '\tfmt.Println("extra line for chunking")\n'
            "}\n"
        )

    # Add new file
    with open(os.path.join(repo, "handler.go"), "w") as f:
        f.write(
            "package main\n\n"
            "// Handler handles HTTP requests for the overlay test.\n"
            "func Handler() string {\n"
            '\treturn "handled"\n'
            "}\n"
        )

    # Delete README.md
    os.remove(os.path.join(repo, "README.md"))

    git("add", ".")
    git("commit", "-m", "Feature branch changes")

    return repo


@pytest.fixture(autouse=True)
def cleanup_overlay():
    """Ensure overlay table and meta row are cleaned up after each test."""
    yield
    # Cleanup — ignore errors if table/row don't exist
    try:
        conn = psycopg2.connect(DATABASE_URL)
        try:
            with conn.cursor() as cur:
                cur.execute(f"DROP TABLE IF EXISTS {TABLE_NAME}")
                cur.execute(
                    "DELETE FROM overlay_meta WHERE engine_id = %s",
                    (ENGINE_ID,),
                )
            conn.commit()
        finally:
            conn.close()
    except Exception:
        pass


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


class TestOverlayBuild:
    """Test overlay.py build with a real git repo and pgvector."""

    @pytest.mark.skipif(
        not _st_available,
        reason="sentence_transformers not installed",
    )
    def test_build_indexes_changed_files(self, git_repo):
        """Build should index changed .go files and populate overlay_meta."""
        result = _run_overlay([
            "build",
            "--engine-id", ENGINE_ID,
            "--worktree", git_repo,
            "--track", TRACK,
            "--file-patterns", "*.go",
            "--database-url", DATABASE_URL,
        ])

        # Verify JSON output
        assert result["files_indexed"] == 2  # hello.go (modified) + handler.go (new)
        assert result["chunks_indexed"] >= 2  # at least 1 chunk per file
        assert result.get("table") == TABLE_NAME

        # Verify overlay table was created with embeddings
        assert _table_exists(TABLE_NAME), f"Table {TABLE_NAME} should exist"
        rows = _query_db(f"SELECT filename, location, code FROM {TABLE_NAME}")
        assert len(rows) >= 2, f"Expected >=2 rows, got {len(rows)}"

        filenames = {r[0] for r in rows}
        assert "hello.go" in filenames
        assert "handler.go" in filenames

        # Verify overlay_meta was populated
        meta = _overlay_meta_row(ENGINE_ID)
        assert meta is not None, "overlay_meta row should exist"
        assert meta["engine_id"] == ENGINE_ID
        assert meta["track"] == TRACK
        assert meta["branch"] == "feature/overlay-test"
        assert meta["files_indexed"] == 2
        assert meta["chunks_indexed"] >= 2
        assert meta["last_commit"] is not None
        # README.md was deleted but doesn't match *.go pattern, so not in deleted_files
        assert meta["deleted_files"] == []

    @pytest.mark.skipif(
        not _st_available,
        reason="sentence_transformers not installed",
    )
    def test_build_tracks_deleted_files(self, git_repo):
        """Build with *.md pattern should report README.md as deleted."""
        result = _run_overlay([
            "build",
            "--engine-id", ENGINE_ID,
            "--worktree", git_repo,
            "--track", TRACK,
            "--file-patterns", "*.md",
            "--database-url", DATABASE_URL,
        ])

        # No .md files changed (only deleted), so no chunks indexed
        assert result["files_indexed"] == 0

        meta = _overlay_meta_row(ENGINE_ID)
        assert meta is not None
        assert "README.md" in meta["deleted_files"]

    @pytest.mark.skipif(
        not _st_available,
        reason="sentence_transformers not installed",
    )
    def test_build_no_changes(self, git_repo):
        """Build with a pattern matching no files should return no_changes."""
        result = _run_overlay([
            "build",
            "--engine-id", ENGINE_ID,
            "--worktree", git_repo,
            "--track", TRACK,
            "--file-patterns", "*.rs",  # no Rust files
            "--database-url", DATABASE_URL,
        ])

        assert result["status"] == "no_changes"
        assert result["files_indexed"] == 0
        assert result["chunks_indexed"] == 0

    @pytest.mark.skipif(
        not _st_available,
        reason="sentence_transformers not installed",
    )
    def test_build_is_idempotent(self, git_repo):
        """Running build twice should produce the same result (truncate + reinsert)."""
        args = [
            "build",
            "--engine-id", ENGINE_ID,
            "--worktree", git_repo,
            "--track", TRACK,
            "--file-patterns", "*.go",
            "--database-url", DATABASE_URL,
        ]
        result1 = _run_overlay(args)
        result2 = _run_overlay(args)

        assert result1["files_indexed"] == result2["files_indexed"]
        assert result1["chunks_indexed"] == result2["chunks_indexed"]

        # Table should still have the same number of rows (truncated + reinserted)
        rows = _query_db(f"SELECT COUNT(*) FROM {TABLE_NAME}")
        assert rows[0][0] == result2["chunks_indexed"]


class TestOverlayStatus:
    """Test overlay.py status with real pgvector."""

    def test_status_not_found(self):
        """Status for non-existent engine should return not_found."""
        result = _run_overlay([
            "status",
            "--engine-id", ENGINE_ID,
            "--database-url", DATABASE_URL,
        ])

        assert result["status"] == "not_found"
        assert result["engine_id"] == ENGINE_ID

    @pytest.mark.skipif(
        not _st_available,
        reason="sentence_transformers not installed",
    )
    def test_status_after_build(self, git_repo):
        """Status after build should return metadata."""
        # Build first
        _run_overlay([
            "build",
            "--engine-id", ENGINE_ID,
            "--worktree", git_repo,
            "--track", TRACK,
            "--file-patterns", "*.go",
            "--database-url", DATABASE_URL,
        ])

        # Check status
        result = _run_overlay([
            "status",
            "--engine-id", ENGINE_ID,
            "--database-url", DATABASE_URL,
        ])

        assert result["status"] == "ok"
        assert result["engine_id"] == ENGINE_ID
        assert result["track"] == TRACK
        assert result["branch"] == "feature/overlay-test"
        assert result["files_indexed"] == 2
        assert result["chunks_indexed"] >= 2
        assert result["last_commit"] is not None
        assert result["created_at"] is not None
        assert result["updated_at"] is not None


class TestOverlayCleanup:
    """Test overlay.py cleanup with real pgvector."""

    @pytest.mark.skipif(
        not _st_available,
        reason="sentence_transformers not installed",
    )
    def test_cleanup_drops_table_and_meta(self, git_repo):
        """Cleanup should drop the overlay table and remove the meta row."""
        # Build first
        _run_overlay([
            "build",
            "--engine-id", ENGINE_ID,
            "--worktree", git_repo,
            "--track", TRACK,
            "--file-patterns", "*.go",
            "--database-url", DATABASE_URL,
        ])

        # Verify table and meta exist
        assert _table_exists(TABLE_NAME)
        assert _overlay_meta_row(ENGINE_ID) is not None

        # Cleanup
        result = _run_overlay([
            "cleanup",
            "--engine-id", ENGINE_ID,
            "--database-url", DATABASE_URL,
        ])

        assert result["status"] == "cleaned"
        assert result["engine_id"] == ENGINE_ID

        # Verify table and meta are gone
        assert not _table_exists(TABLE_NAME), "Table should be dropped"
        assert _overlay_meta_row(ENGINE_ID) is None, "Meta row should be deleted"

    def test_cleanup_idempotent(self):
        """Cleanup on non-existent overlay should succeed (idempotent)."""
        result = _run_overlay([
            "cleanup",
            "--engine-id", ENGINE_ID,
            "--database-url", DATABASE_URL,
        ])

        assert result["status"] == "cleaned"


class TestFullLifecycle:
    """End-to-end test: build -> status -> verify data -> cleanup -> verify gone."""

    @pytest.mark.skipif(
        not _st_available,
        reason="sentence_transformers not installed",
    )
    def test_full_lifecycle(self, git_repo):
        # 1. Build overlay
        build_result = _run_overlay([
            "build",
            "--engine-id", ENGINE_ID,
            "--worktree", git_repo,
            "--track", TRACK,
            "--file-patterns", "*.go",
            "--database-url", DATABASE_URL,
        ])
        assert build_result["files_indexed"] == 2

        # 2. Verify table has embeddings with correct vector dimensions
        rows = _query_db(
            f"SELECT filename, embedding FROM {TABLE_NAME} LIMIT 1"
        )
        assert len(rows) == 1
        # embedding column should be a vector(384)
        embedding_str = str(rows[0][1])
        assert embedding_str.startswith("[")

        # 3. Status should report ok
        status_result = _run_overlay([
            "status",
            "--engine-id", ENGINE_ID,
            "--database-url", DATABASE_URL,
        ])
        assert status_result["status"] == "ok"
        assert status_result["chunks_indexed"] == build_result["chunks_indexed"]

        # 4. Verify we can query by vector similarity (basic smoke test)
        # Use the first embedding to find similar chunks
        rows = _query_db(
            f"SELECT filename, code, 1 - (embedding <=> embedding) AS score "
            f"FROM {TABLE_NAME} ORDER BY score DESC LIMIT 5"
        )
        assert len(rows) >= 1
        # Self-similarity should be 1.0
        assert abs(rows[0][2] - 1.0) < 0.001

        # 5. Cleanup
        cleanup_result = _run_overlay([
            "cleanup",
            "--engine-id", ENGINE_ID,
            "--database-url", DATABASE_URL,
        ])
        assert cleanup_result["status"] == "cleaned"

        # 6. Verify everything is gone
        assert not _table_exists(TABLE_NAME)
        assert _overlay_meta_row(ENGINE_ID) is None

        # 7. Status should report not_found
        status_result = _run_overlay([
            "status",
            "--engine-id", ENGINE_ID,
            "--database-url", DATABASE_URL,
        ])
        assert status_result["status"] == "not_found"
