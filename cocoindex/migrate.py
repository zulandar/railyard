"""
Apply pgvector schema migrations for Railyard's CocoIndex integration.

Runs SQL migration files from the migrations/ directory in order.
Tracks applied migrations in a _migrations table to avoid re-running.

Usage:
    python migrate.py --database-url postgresql://cocoindex:cocoindex@localhost:5481/cocoindex
"""

import argparse
import glob
import os
import sys


MIGRATIONS_DIR = os.path.join(os.path.dirname(__file__), "migrations")


def get_migration_files() -> list[tuple[str, str]]:
    """Return (name, path) pairs for SQL files in migrations/, sorted by name."""
    pattern = os.path.join(MIGRATIONS_DIR, "*.sql")
    files = sorted(glob.glob(pattern))
    return [(os.path.basename(f), f) for f in files]


def ensure_migrations_table(conn) -> None:
    """Create the _migrations tracking table if it doesn't exist."""
    with conn.cursor() as cur:
        cur.execute("""
            CREATE TABLE IF NOT EXISTS _migrations (
                name        TEXT PRIMARY KEY,
                applied_at  TIMESTAMP DEFAULT NOW()
            )
        """)
    conn.commit()


def get_applied_migrations(conn) -> set[str]:
    """Return the set of migration names already applied."""
    with conn.cursor() as cur:
        cur.execute("SELECT name FROM _migrations")
        return {row[0] for row in cur.fetchall()}


def apply_migration(conn, name: str, path: str) -> None:
    """Execute a single SQL migration file and record it."""
    with open(path) as f:
        sql = f.read()
    with conn.cursor() as cur:
        cur.execute(sql)
        cur.execute(
            "INSERT INTO _migrations (name) VALUES (%s)",
            (name,),
        )
    conn.commit()


def run_migrations(database_url: str) -> list[str]:
    """Apply all pending migrations. Returns list of newly applied names."""
    import psycopg2

    conn = psycopg2.connect(database_url)
    try:
        ensure_migrations_table(conn)
        applied = get_applied_migrations(conn)
        newly_applied = []

        for name, path in get_migration_files():
            if name in applied:
                continue
            print(f"Applying {name}...")
            apply_migration(conn, name, path)
            newly_applied.append(name)
            print(f"  Done.")

        if not newly_applied:
            print("All migrations already applied.")
        else:
            print(f"\n{len(newly_applied)} migration(s) applied.")

        return newly_applied
    finally:
        conn.close()


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Apply pgvector schema migrations for CocoIndex."
    )
    parser.add_argument(
        "--database-url", required=True,
        help="PostgreSQL connection URL for the pgvector database.",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> None:
    args = parse_args(argv)
    run_migrations(args.database_url)


if __name__ == "__main__":
    main()
