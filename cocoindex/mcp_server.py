"""
Railyard CocoIndex MCP Server — semantic code search for engines.

Provides dual-table search (main index + overlay) with overlay-wins
deduplication. Accepts engine identity via environment variables.
Falls back to main-table-only search when overlay env vars are absent.

Launched per-engine via .mcp.json with stdio transport.

Environment variables:
    COCOINDEX_DATABASE_URL  — pgvector connection string (required)
    COCOINDEX_ENGINE_ID     — engine ID (e.g. eng-a1b2c3d4)
    COCOINDEX_MAIN_TABLE    — main index table (e.g. main_backend_embeddings)
    COCOINDEX_OVERLAY_TABLE — overlay table (e.g. ovl_eng_a1b2c3d4; empty if none)
    COCOINDEX_TRACK         — track name (e.g. backend)
    COCOINDEX_WORKTREE      — worktree path

Usage:
    COCOINDEX_DATABASE_URL=postgresql://... python mcp_server.py
"""

import json
import os
import subprocess
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass

import psycopg2
from sentence_transformers import SentenceTransformer

from main import EMBEDDING_MODEL

try:
    from mcp.server.fastmcp import FastMCP
except ImportError:
    FastMCP = None  # allows importing for tests without mcp installed


# ---------------------------------------------------------------------------
# Configuration from environment
# ---------------------------------------------------------------------------


@dataclass
class ServerConfig:
    """Configuration loaded from environment variables."""
    database_url: str
    engine_id: str | None = None
    main_table: str | None = None
    main_tables: list[str] | None = None  # comma-separated COCOINDEX_MAIN_TABLE
    overlay_table: str | None = None
    track: str | None = None
    worktree: str | None = None


def load_server_config() -> ServerConfig:
    """Load server configuration from environment variables.

    COCOINDEX_MAIN_TABLE can be a single table name or comma-separated list
    (e.g. "main_backend_embeddings,main_frontend_embeddings") for cross-track
    search in the dispatcher.
    """
    database_url = os.environ.get("COCOINDEX_DATABASE_URL", "")
    if not database_url:
        raise ValueError("COCOINDEX_DATABASE_URL environment variable is required")

    raw_tables = os.environ.get("COCOINDEX_MAIN_TABLE") or None
    main_table = None
    main_tables = None
    if raw_tables and "," in raw_tables:
        main_tables = [t.strip() for t in raw_tables.split(",") if t.strip()]
        main_table = main_tables[0] if main_tables else None
    else:
        main_table = raw_tables

    return ServerConfig(
        database_url=database_url,
        engine_id=os.environ.get("COCOINDEX_ENGINE_ID") or None,
        main_table=main_table,
        main_tables=main_tables,
        overlay_table=os.environ.get("COCOINDEX_OVERLAY_TABLE") or None,
        track=os.environ.get("COCOINDEX_TRACK") or None,
        worktree=os.environ.get("COCOINDEX_WORKTREE") or None,
    )


# ---------------------------------------------------------------------------
# Embedding helper
# ---------------------------------------------------------------------------

_model: SentenceTransformer | None = None


def get_model() -> SentenceTransformer:
    """Lazy-load the embedding model (same as indexer for consistency)."""
    global _model
    if _model is None:
        _model = SentenceTransformer(EMBEDDING_MODEL)
    return _model


def embed_query(query: str) -> list[float]:
    """Embed a search query string into a vector."""
    model = get_model()
    return model.encode(query).tolist()


# ---------------------------------------------------------------------------
# Database queries
# ---------------------------------------------------------------------------


def query_table(
    database_url: str,
    table: str,
    embedding: list[float],
    top_k: int = 10,
    min_score: float = 0.0,
) -> list[dict]:
    """Query a pgvector table for similar embeddings.

    Returns list of {filename, code, location, score}.
    """
    embedding_str = "[" + ",".join(str(x) for x in embedding) + "]"
    conn = psycopg2.connect(database_url)
    try:
        with conn.cursor() as cur:
            cur.execute(
                f"SELECT filename, code, location, "
                f"1 - (embedding <=> %s::vector) AS score "
                f"FROM {table} "
                f"ORDER BY embedding <=> %s::vector "
                f"LIMIT %s",
                (embedding_str, embedding_str, top_k * 2),
            )
            rows = cur.fetchall()
    finally:
        conn.close()

    results = []
    for filename, code, location, score in rows:
        if score >= min_score:
            results.append({
                "filename": filename,
                "code": code,
                "location": location,
                "score": round(float(score), 4),
            })
    return results


def get_deleted_files(database_url: str, engine_id: str) -> list[str]:
    """Load deleted_files list from overlay_meta for an engine."""
    conn = psycopg2.connect(database_url)
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT deleted_files FROM overlay_meta WHERE engine_id = %s",
                (engine_id,),
            )
            row = cur.fetchone()
    finally:
        conn.close()

    if row is None or row[0] is None:
        return []
    return json.loads(row[0])


# ---------------------------------------------------------------------------
# Merge algorithm
# ---------------------------------------------------------------------------


def merge_results(
    main_results: list[dict],
    overlay_results: list[dict],
    deleted_files: list[str],
    top_k: int = 10,
    min_score: float = 0.0,
) -> list[dict]:
    """Merge main and overlay results with overlay-wins deduplication.

    Algorithm:
    1. Index overlay results by (filename, location) — these take precedence
    2. Add all overlay results to merged set
    3. For each main result:
       - Skip if (filename, location) already in merged set (overlay wins)
       - Skip if filename is in deleted_files
       - Otherwise add to merged set
    4. Sort by score descending
    5. Filter by min_score, return top_k
    """
    deleted_set = set(deleted_files)

    # Index overlay results by key
    merged = {}
    for r in overlay_results:
        key = (r["filename"], r["location"])
        merged[key] = r

    # Add main results (overlay wins on conflict)
    for r in main_results:
        if r["filename"] in deleted_set:
            continue
        key = (r["filename"], r["location"])
        if key in merged:
            continue  # overlay wins
        merged[key] = r

    # Sort by score descending, filter, limit
    sorted_results = sorted(merged.values(), key=lambda r: r["score"], reverse=True)
    filtered = [r for r in sorted_results if r["score"] >= min_score]
    return filtered[:top_k]


# ---------------------------------------------------------------------------
# Search implementation
# ---------------------------------------------------------------------------


def search(
    config: ServerConfig,
    query: str,
    top_k: int = 10,
    min_score: float = 0.0,
) -> list[dict]:
    """Run dual-table search with overlay merge.

    If overlay_table is configured, queries both tables in parallel and
    merges with overlay-wins deduplication. Otherwise queries main only.

    Supports multiple main tables (e.g. for cross-track dispatcher search):
    queries all tables in parallel and merges results by score.
    """
    embedding = embed_query(query)

    if not config.main_table and not config.main_tables:
        return []

    # Multi-table search (dispatcher mode): query all main tables in parallel.
    if config.main_tables and not config.overlay_table:
        with ThreadPoolExecutor(max_workers=len(config.main_tables)) as pool:
            futures = [
                pool.submit(
                    query_table, config.database_url, table,
                    embedding, top_k, min_score,
                )
                for table in config.main_tables
            ]
            all_results = []
            for f in futures:
                try:
                    all_results.extend(f.result())
                except Exception:
                    pass  # skip tables that don't exist yet
        # Deduplicate by (filename, location), keep highest score.
        seen = {}
        for r in all_results:
            key = (r["filename"], r["location"])
            if key not in seen or r["score"] > seen[key]["score"]:
                seen[key] = r
        sorted_results = sorted(seen.values(), key=lambda r: r["score"], reverse=True)
        return [r for r in sorted_results if r["score"] >= min_score][:top_k]

    # Single-table, no overlay: just query main.
    if not config.overlay_table:
        results = query_table(
            config.database_url, config.main_table, embedding, top_k, min_score,
        )
        return results[:top_k]

    # Parallel query: main + overlay
    with ThreadPoolExecutor(max_workers=2) as pool:
        main_future = pool.submit(
            query_table, config.database_url, config.main_table,
            embedding, top_k, min_score,
        )
        overlay_future = pool.submit(
            query_table, config.database_url, config.overlay_table,
            embedding, top_k, min_score,
        )
        main_results = main_future.result()
        overlay_results = overlay_future.result()

    # Get deleted files for dedup
    deleted = []
    if config.engine_id:
        deleted = get_deleted_files(config.database_url, config.engine_id)

    return merge_results(main_results, overlay_results, deleted, top_k, min_score)


# ---------------------------------------------------------------------------
# Overlay status / refresh
# ---------------------------------------------------------------------------


def get_overlay_status(config: ServerConfig) -> dict:
    """Query overlay_meta for engine status."""
    if not config.engine_id:
        return {"status": "no_engine_id"}

    conn = psycopg2.connect(config.database_url)
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT engine_id, track, branch, last_commit, "
                "files_indexed, chunks_indexed, deleted_files, "
                "created_at, updated_at "
                "FROM overlay_meta WHERE engine_id = %s",
                (config.engine_id,),
            )
            row = cur.fetchone()
    finally:
        conn.close()

    if row is None:
        return {"engine_id": config.engine_id, "status": "not_found"}

    return {
        "engine_id": row[0],
        "track": row[1],
        "branch": row[2],
        "last_commit": row[3],
        "files_indexed": row[4],
        "chunks_indexed": row[5],
        "deleted_files": json.loads(row[6]) if row[6] else [],
        "created_at": str(row[7]),
        "updated_at": str(row[8]),
        "status": "ok",
    }


# Rate limiting for refresh_overlay
_last_refresh_time: float = 0.0
REFRESH_COOLDOWN_SEC = 30


def refresh_overlay(config: ServerConfig) -> dict:
    """Rebuild overlay index by calling overlay.py build as subprocess.

    Rate-limited to max once per 30 seconds.
    """
    global _last_refresh_time

    if not config.engine_id or not config.worktree or not config.track:
        return {"status": "error", "message": "Missing engine_id, worktree, or track"}

    now = time.time()
    if now - _last_refresh_time < REFRESH_COOLDOWN_SEC:
        remaining = int(REFRESH_COOLDOWN_SEC - (now - _last_refresh_time))
        return {"status": "rate_limited", "retry_after_sec": remaining}

    _last_refresh_time = now
    start = time.time()

    # Build the overlay.py command
    script_dir = os.path.dirname(os.path.abspath(__file__))
    cmd = [
        "python3", os.path.join(script_dir, "overlay.py"),
        "build",
        "--engine-id", config.engine_id,
        "--worktree", config.worktree,
        "--track", config.track,
        "--file-patterns", "*",  # use all patterns; overlay.py reads config
        "--database-url", config.database_url,
    ]

    result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    duration_ms = int((time.time() - start) * 1000)

    if result.returncode != 0:
        return {
            "status": "error",
            "message": result.stderr.strip(),
            "duration_ms": duration_ms,
        }

    try:
        output = json.loads(result.stdout.strip())
    except (json.JSONDecodeError, ValueError):
        output = {}

    return {
        "status": "ok",
        "files_indexed": output.get("files_indexed", 0),
        "chunks_indexed": output.get("chunks_indexed", 0),
        "duration_ms": duration_ms,
    }


# ---------------------------------------------------------------------------
# MCP Server
# ---------------------------------------------------------------------------


def create_server(config: ServerConfig | None = None) -> "FastMCP":
    """Create and configure the MCP server with all tools."""
    if FastMCP is None:
        raise ImportError("mcp package is required: pip install mcp")

    if config is None:
        config = load_server_config()

    mcp = FastMCP("railyard-cocoindex")

    @mcp.tool()
    def search_code(query: str, top_k: int = 10, min_score: float = 0.0) -> list[dict]:
        """Search the codebase using semantic similarity.

        Returns code snippets ranked by relevance to your query.
        Automatically searches both the main index and any overlay
        index for branch-modified files.

        Args:
            query: Natural language description of what you're looking for
            top_k: Maximum number of results to return (default: 10)
            min_score: Minimum cosine similarity score (0.0-1.0, default: 0.0)
        """
        return search(config, query, top_k=top_k, min_score=min_score)

    @mcp.tool()
    def overlay_status() -> dict:
        """Get the status of the overlay index for this engine.

        Returns metadata about the overlay including track, branch,
        last indexed commit, file counts, and freshness.
        """
        return get_overlay_status(config)

    @mcp.tool()
    def overlay_refresh() -> dict:
        """Rebuild the overlay index for this engine's branch changes.

        Re-indexes files that differ between the engine's branch and main.
        Rate-limited to once per 30 seconds.
        """
        return refresh_overlay(config)

    return mcp


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------


if __name__ == "__main__":
    server = create_server()
    server.run()
