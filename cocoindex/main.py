"""
Per-track main index builder for Railyard.

Creates a CocoIndex flow that indexes source files into a pgvector table
named main_{track}_embeddings. The code_to_embedding transform is defined
at module level so overlay.py can import it for vector space consistency.

Usage:
    python main.py --track backend --file-patterns "*.go" "cmd/**" "internal/**"
    python main.py --track frontend --file-patterns "*.ts" "*.tsx" "src/**"
"""

import argparse
import os
import sys

import cocoindex
import numpy as np
from numpy.typing import NDArray

# ---------------------------------------------------------------------------
# Shared embedding transform — importable by overlay.py
# ---------------------------------------------------------------------------

EMBEDDING_MODEL = "sentence-transformers/all-MiniLM-L6-v2"


@cocoindex.transform_flow()
def code_to_embedding(
    text: cocoindex.DataSlice[str],
) -> cocoindex.DataSlice[NDArray[np.float32]]:
    """Embed a text chunk. Reuse this in overlay.py for vector space consistency."""
    return text.transform(
        cocoindex.functions.SentenceTransformerEmbed(model=EMBEDDING_MODEL)
    )


# ---------------------------------------------------------------------------
# Language mapping — maps railyard track languages to tree-sitter languages
# ---------------------------------------------------------------------------

LANGUAGE_MAP = {
    "go": "go",
    "typescript": "typescript",
    "javascript": "javascript",
    "python": "python",
    "rust": "rust",
    "java": "java",
    "c": "c",
    "cpp": "cpp",
    "ruby": "ruby",
    "swift": "swift",
    "mixed": None,  # falls back to plain text splitting
}

# Patterns always excluded from indexing
EXCLUDED_PATTERNS = [".*", "vendor", "node_modules", "dist", "__pycache__", ".git"]


# ---------------------------------------------------------------------------
# Flow definition
# ---------------------------------------------------------------------------


def make_flow_def(
    track_name: str,
    file_patterns: list[str],
    repo_path: str,
    language: str | None = None,
):
    """Return a CocoIndex flow definition function for the given track."""
    table_name = f"main_{track_name}_embeddings"
    treesitter_lang = LANGUAGE_MAP.get(language) if language else None

    def flow_def(
        flow_builder: cocoindex.FlowBuilder, data_scope: cocoindex.DataScope
    ):
        data_scope["files"] = flow_builder.add_source(
            cocoindex.sources.LocalFile(
                path=repo_path,
                included_patterns=file_patterns,
                excluded_patterns=EXCLUDED_PATTERNS,
            ),
            refresh_interval=None,  # triggered manually after merge
        )

        code_embeddings = data_scope.add_collector()

        with data_scope["files"].row() as doc:
            split_kwargs = {"chunk_size": 1500, "chunk_overlap": 300}
            if treesitter_lang:
                split_kwargs["language"] = treesitter_lang

            doc["chunks"] = doc["content"].transform(
                cocoindex.functions.SplitRecursively(),
                **split_kwargs,
            )

            with doc["chunks"].row() as chunk:
                chunk["embedding"] = code_to_embedding(chunk["text"])
                code_embeddings.collect(
                    filename=doc["filename"],
                    location=chunk["location"],
                    code=chunk["text"],
                    embedding=chunk["embedding"],
                )

        code_embeddings.export(
            "code_embeddings",
            cocoindex.targets.Postgres(table_name=table_name),
            primary_key_fields=["filename", "location"],
            vector_indexes=[
                cocoindex.VectorIndexDef(
                    field_name="embedding",
                    metric=cocoindex.VectorSimilarityMetric.COSINE_SIMILARITY,
                )
            ],
        )

    return flow_def


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build a per-track CocoIndex main embedding index."
    )
    parser.add_argument(
        "--track",
        required=True,
        help="Track name (e.g. backend, frontend). Used in table name.",
    )
    parser.add_argument(
        "--file-patterns",
        nargs="+",
        required=True,
        help='Glob patterns for files to index (e.g. "*.go" "cmd/**").',
    )
    parser.add_argument(
        "--repo-path",
        default=".",
        help="Path to the repository root (default: current directory).",
    )
    parser.add_argument(
        "--language",
        default=None,
        help="Primary language for tree-sitter parsing (go, typescript, python, etc.).",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> None:
    args = parse_args(argv)

    cocoindex.init()

    flow_name = f"CodeEmbedding_{args.track}"
    flow_def = make_flow_def(
        track_name=args.track,
        file_patterns=args.file_patterns,
        repo_path=args.repo_path,
        language=args.language,
    )

    flow = cocoindex.open_flow(flow_name, flow_def)
    cocoindex.setup_all_flows()
    print(f"Indexing track '{args.track}' -> main_{args.track}_embeddings")
    flow.update()
    print(f"Done. Table main_{args.track}_embeddings is up to date.")


if __name__ == "__main__":
    main()
