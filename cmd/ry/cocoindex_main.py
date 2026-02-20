"""
Per-track main index builder for Railyard.

Creates a CocoIndex flow that indexes source files into a pgvector table
named main_{track}_embeddings (configurable via cocoindex.yaml). The
code_to_embedding transform is defined at module level so overlay.py can
import it for vector space consistency.

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

from config import CocoIndexConfig, load_config

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
    cfg: CocoIndexConfig | None = None,
):
    """Return a CocoIndex flow definition function for the given track.

    When cfg is provided, table name and excluded patterns are resolved
    from the CocoIndex config (with per-track overrides). Otherwise falls
    back to hardcoded defaults for backward compatibility.
    """
    # CocoIndex LocalFile requires the full absolute path to the root directory.
    repo_path = os.path.abspath(repo_path)

    if cfg is not None:
        table_name = cfg.main_table_name(track_name)
        excluded = cfg.excluded_patterns_for_track(track_name)
        file_patterns = cfg.included_patterns_for_track(track_name, file_patterns)
    else:
        table_name = f"main_{track_name}_embeddings"
        excluded = EXCLUDED_PATTERNS

    treesitter_lang = LANGUAGE_MAP.get(language) if language else None

    def flow_def(
        flow_builder: cocoindex.FlowBuilder, data_scope: cocoindex.DataScope
    ):
        data_scope["files"] = flow_builder.add_source(
            cocoindex.sources.LocalFile(
                path=repo_path,
                included_patterns=file_patterns,
                excluded_patterns=excluded,
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
    parser.add_argument(
        "--config",
        default=None,
        help="Path to cocoindex.yaml config file (auto-detected if omitted).",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        default=False,
        help="Force reprocessing even if data appears up-to-date.",
    )
    return parser.parse_args(argv)


def build_index(
    track: str,
    file_patterns: list[str],
    repo_path: str,
    language: str | None = None,
    config_path: str | None = None,
    force: bool = False,
) -> None:
    """Build the index for a single track.

    Assumes cocoindex.init() has already been called. Opens the flow,
    sets up schemas, runs update, and prints stats.
    """
    cfg = load_config(config_path)
    table_name = cfg.main_table_name(track)
    flow_name = f"CodeEmbedding_{track}"
    abs_repo = os.path.abspath(repo_path)

    flow_def = make_flow_def(
        track_name=track,
        file_patterns=file_patterns,
        repo_path=abs_repo,
        language=language,
        cfg=cfg,
    )

    flow = cocoindex.open_flow(flow_name, flow_def)
    cocoindex.setup_all_flows()

    print(f"Indexing track '{track}' -> {table_name}")
    print(f"  Source path: {abs_repo}")
    print(f"  Patterns:    {file_patterns}")
    if force:
        print("  Mode:        force (reexport_targets=True)")

    stats = flow.update(reexport_targets=force)
    print(f"  Stats:       {stats}")
    print(f"Done. Table {table_name} is up to date.")


def main(argv: list[str] | None = None) -> None:
    args = parse_args(argv)

    cocoindex.init()

    build_index(
        track=args.track,
        file_patterns=args.file_patterns,
        repo_path=args.repo_path,
        language=args.language,
        config_path=args.config,
        force=args.force,
    )


if __name__ == "__main__":
    main()
