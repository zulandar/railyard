"""
Overlay indexer for Railyard engines.

Indexes only files that differ between an engine's branch and main into a
per-engine pgvector table (ovl_{engine_id}). Invoked as a one-shot subprocess
by the Go engine daemon. Uses the same embedding model as main.py for vector
space consistency.

Usage:
    python overlay.py build --engine-id eng-abc123 --worktree /path --track backend \
        --file-patterns "*.go" "cmd/**" --database-url postgresql://...
"""

import argparse
import fnmatch
import json
import os
import re
import subprocess
import sys

from config import load_config
from main import EMBEDDING_MODEL, LANGUAGE_MAP

# ---------------------------------------------------------------------------
# Table name sanitization
# ---------------------------------------------------------------------------

_SAFE_ID_RE = re.compile(r"^[a-zA-Z0-9_-]+$")


def overlay_table_name(engine_id: str, prefix: str = "ovl_") -> str:
    """Derive a safe Postgres table name from an engine ID.

    eng-a1b2c3d4 -> ovl_eng_a1b2c3d4 (default prefix)
    """
    if not _SAFE_ID_RE.match(engine_id):
        raise ValueError(f"invalid engine_id: {engine_id!r}")
    return prefix + engine_id.replace("-", "_")


# ---------------------------------------------------------------------------
# Git helpers
# ---------------------------------------------------------------------------


def get_changed_files(worktree: str) -> list[str]:
    """Files changed between main and HEAD (added + modified)."""
    result = subprocess.run(
        ["git", "diff", "--name-only", "main...HEAD"],
        cwd=worktree, capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise RuntimeError(f"git diff failed: {result.stderr.strip()}")
    raw = result.stdout.strip()
    return raw.split("\n") if raw else []


def get_deleted_files(worktree: str) -> list[str]:
    """Files deleted between main and HEAD."""
    result = subprocess.run(
        ["git", "diff", "--name-only", "--diff-filter=D", "main...HEAD"],
        cwd=worktree, capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise RuntimeError(f"git diff failed: {result.stderr.strip()}")
    raw = result.stdout.strip()
    return raw.split("\n") if raw else []


def get_head_commit(worktree: str) -> str:
    """Current HEAD commit hash."""
    result = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=worktree, capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise RuntimeError(f"git rev-parse failed: {result.stderr.strip()}")
    return result.stdout.strip()


def get_current_branch(worktree: str) -> str:
    """Current branch name (best-effort)."""
    result = subprocess.run(
        ["git", "rev-parse", "--abbrev-ref", "HEAD"],
        cwd=worktree, capture_output=True, text=True,
    )
    if result.returncode != 0:
        return "unknown"
    return result.stdout.strip()


# ---------------------------------------------------------------------------
# File filtering
# ---------------------------------------------------------------------------


def filter_by_patterns(files: list[str], patterns: list[str]) -> list[str]:
    """Keep only files matching at least one glob pattern."""
    matched = []
    for f in files:
        for pattern in patterns:
            if fnmatch.fnmatch(f, pattern):
                matched.append(f)
                break
    return matched


# ---------------------------------------------------------------------------
# Text chunking
# ---------------------------------------------------------------------------


def chunk_text(
    text: str, chunk_size: int = 1500, chunk_overlap: int = 300,
) -> list[dict]:
    """Split text into overlapping chunks.

    Returns list of {"text": ..., "location": "idx:offset"}.
    Tries to break at newline boundaries for cleaner chunks.
    """
    if not text.strip():
        return []
    if len(text) <= chunk_size:
        return [{"text": text, "location": "0:0"}]

    chunks = []
    start = 0
    chunk_idx = 0
    while start < len(text):
        end = min(start + chunk_size, len(text))
        # Try to break at a newline boundary within the last quarter
        if end < len(text):
            search_start = start + (chunk_size * 3 // 4)
            newline_pos = text.rfind("\n", search_start, end)
            if newline_pos > start:
                end = newline_pos + 1
        chunk_str = text[start:end]
        if chunk_str.strip():
            chunks.append({
                "text": chunk_str,
                "location": f"{chunk_idx}:{start}",
            })
            chunk_idx += 1
        # Advance with overlap
        next_start = end - chunk_overlap
        if next_start <= start:
            next_start = end
        start = next_start

    return chunks


# ---------------------------------------------------------------------------
# Build subcommand
# ---------------------------------------------------------------------------


def build(args: argparse.Namespace) -> dict:
    """Build overlay index for files changed on the engine's branch."""
    # Lazy import — only needed at runtime, not when testing pure functions
    import psycopg2
    from sentence_transformers import SentenceTransformer

    # Load config for overlay table prefix
    cfg = load_config(getattr(args, "config", None))

    # 1. Get changed and deleted files via git
    changed = get_changed_files(args.worktree)
    deleted = get_deleted_files(args.worktree)

    # 2. Filter by track's file patterns (use config overrides if present)
    patterns = cfg.included_patterns_for_track(args.track, args.file_patterns)
    changed = filter_by_patterns(changed, patterns)
    deleted = filter_by_patterns(deleted, patterns)

    if not changed and not deleted:
        result = {"files_indexed": 0, "chunks_indexed": 0, "status": "no_changes"}
        print(json.dumps(result))
        return result

    # 3. Load embedding model (same as main index for vector space consistency)
    model = SentenceTransformer(EMBEDDING_MODEL)

    # 4. Read, chunk, and embed changed files
    rows = []
    for filepath in changed:
        full_path = os.path.join(args.worktree, filepath)
        if not os.path.exists(full_path):
            continue
        try:
            with open(full_path, encoding="utf-8", errors="replace") as f:
                content = f.read()
        except OSError:
            continue
        chunks = chunk_text(content)
        for chunk in chunks:
            embedding = model.encode(chunk["text"]).tolist()
            rows.append((filepath, chunk["location"], chunk["text"], embedding))

    # 5-6. Create/truncate overlay table and insert embeddings
    table = overlay_table_name(args.engine_id, prefix=cfg.overlay_table_prefix)
    conn = psycopg2.connect(args.database_url)
    try:
        with conn.cursor() as cur:
            cur.execute(f"""
                CREATE TABLE IF NOT EXISTS {table} (
                    filename    TEXT NOT NULL,
                    location    TEXT,
                    code        TEXT NOT NULL,
                    embedding   vector(384),
                    PRIMARY KEY (filename, location)
                )
            """)
            cur.execute(f"TRUNCATE TABLE {table}")

            for filename, location, code, embedding in rows:
                embedding_str = "[" + ",".join(str(x) for x in embedding) + "]"
                cur.execute(
                    f"INSERT INTO {table} (filename, location, code, embedding) "
                    "VALUES (%s, %s, %s, %s::vector)",
                    (filename, location, code, embedding_str),
                )

            # Create IVFFlat index if enough rows (lists=10 needs >= 10 rows)
            if len(rows) >= 10:
                cur.execute(f"""
                    CREATE INDEX IF NOT EXISTS idx_{table}_embedding
                    ON {table}
                    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 10)
                """)

            # 7. Upsert overlay_meta row
            head = get_head_commit(args.worktree)
            branch = get_current_branch(args.worktree)
            cur.execute("""
                INSERT INTO overlay_meta
                    (engine_id, track, branch, last_commit,
                     files_indexed, chunks_indexed, deleted_files, updated_at)
                VALUES (%s, %s, %s, %s, %s, %s, %s, NOW())
                ON CONFLICT (engine_id) DO UPDATE SET
                    track = EXCLUDED.track,
                    branch = EXCLUDED.branch,
                    last_commit = EXCLUDED.last_commit,
                    files_indexed = EXCLUDED.files_indexed,
                    chunks_indexed = EXCLUDED.chunks_indexed,
                    deleted_files = EXCLUDED.deleted_files,
                    updated_at = NOW()
            """, (
                args.engine_id, args.track, branch, head,
                len(changed), len(rows), json.dumps(deleted),
            ))
        conn.commit()
    finally:
        conn.close()

    result = {
        "files_indexed": len(changed),
        "chunks_indexed": len(rows),
        "deleted_files": len(deleted),
        "table": table,
    }
    print(json.dumps(result))
    return result


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Overlay indexer for Railyard engines.")
    sub = parser.add_subparsers(dest="command", required=True)

    build_p = sub.add_parser("build", help="Build overlay index for changed files.")
    build_p.add_argument("--engine-id", required=True, help="Engine ID (e.g. eng-a1b2c3d4).")
    build_p.add_argument("--worktree", required=True, help="Path to engine's git worktree.")
    build_p.add_argument("--track", required=True, help="Track name (e.g. backend).")
    build_p.add_argument(
        "--file-patterns", nargs="+", required=True,
        help='Glob patterns for this track (e.g. "*.go" "cmd/**").',
    )
    build_p.add_argument("--database-url", required=True, help="pgvector database URL.")
    build_p.add_argument("--language", default=None, help="Primary language (for future use).")
    build_p.add_argument("--config", default=None, help="Path to cocoindex.yaml config file.")

    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> None:
    args = parse_args(argv)
    if args.command == "build":
        build(args)


if __name__ == "__main__":
    main()
