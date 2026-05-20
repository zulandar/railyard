-- Railyard pgvector initialization script.
-- Runs automatically when the container is first created.
-- Enables the vector extension required for CocoIndex embeddings.

CREATE EXTENSION IF NOT EXISTS vector;
