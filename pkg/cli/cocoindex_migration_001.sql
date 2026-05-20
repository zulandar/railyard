-- Migration 001: Create overlay_meta table for tracking per-engine overlay indexes.
-- This table lives in the pgvector PostgreSQL database alongside the embedding tables.
--
-- Run: python migrate.py --database-url postgresql://...

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS overlay_meta (
    engine_id       TEXT PRIMARY KEY,
    track           TEXT NOT NULL,
    branch          TEXT NOT NULL,
    last_commit     TEXT,
    files_indexed   INTEGER DEFAULT 0,
    chunks_indexed  INTEGER DEFAULT 0,
    deleted_files   TEXT DEFAULT '[]',
    created_at      TIMESTAMP DEFAULT NOW(),
    updated_at      TIMESTAMP DEFAULT NOW()
);
