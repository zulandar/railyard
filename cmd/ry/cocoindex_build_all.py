"""
Per-track index builder — iterates all tracks from railyard.yaml and runs
a CocoIndex flow for each.

Reads track definitions (name, language, file_patterns) from railyard.yaml
and the CocoIndex config (table naming, exclusion patterns) from
cocoindex.yaml. Creates one pgvector table per track with IVFFlat index.

Usage:
    python build_all.py --railyard-config ../railyard.yaml
    python build_all.py --railyard-config ../railyard.yaml --tracks backend
    python build_all.py --railyard-config ../railyard.yaml --repo-path /path/to/repo
"""

import argparse
import sys

import cocoindex
import yaml

from config import load_config


def load_tracks(railyard_config_path: str) -> list[dict]:
    """Load track definitions from railyard.yaml.

    Returns list of dicts with keys: name, language, file_patterns.
    """
    with open(railyard_config_path, encoding="utf-8") as f:
        raw = yaml.safe_load(f)

    if not raw or "tracks" not in raw:
        return []

    tracks = []
    for track in raw["tracks"]:
        if not isinstance(track, dict):
            continue
        name = track.get("name")
        if not name:
            continue
        tracks.append({
            "name": name,
            "language": track.get("language"),
            "file_patterns": track.get("file_patterns", []),
        })
    return tracks


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build CocoIndex main indexes for all tracks in railyard.yaml."
    )
    parser.add_argument(
        "--railyard-config",
        required=True,
        help="Path to railyard.yaml with track definitions.",
    )
    parser.add_argument(
        "--repo-path",
        default=".",
        help="Path to the repository root (default: current directory).",
    )
    parser.add_argument(
        "--config",
        default=None,
        help="Path to cocoindex.yaml config file (auto-detected if omitted).",
    )
    parser.add_argument(
        "--tracks",
        nargs="+",
        default=None,
        help="Only build these tracks (default: all tracks in railyard.yaml).",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        default=False,
        help="Force reprocessing even if data appears up-to-date.",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> None:
    args = parse_args(argv)

    tracks = load_tracks(args.railyard_config)
    if not tracks:
        print("No tracks found in railyard.yaml.", file=sys.stderr)
        sys.exit(1)

    cfg = load_config(args.config)

    # Filter tracks if --tracks flag provided
    if args.tracks:
        track_names = set(args.tracks)
        tracks = [t for t in tracks if t["name"] in track_names]
        if not tracks:
            print(
                f"No matching tracks found. Available: "
                f"{', '.join(t['name'] for t in load_tracks(args.railyard_config))}",
                file=sys.stderr,
            )
            sys.exit(1)

    # Initialize CocoIndex once for all tracks.
    cocoindex.init()

    print(f"Building indexes for {len(tracks)} track(s)...")

    from main import build_index

    for track in tracks:
        name = track["name"]
        file_patterns = cfg.included_patterns_for_track(name, track["file_patterns"])
        print(f"\n--- Track: {name} ---")

        build_index(
            track=name,
            file_patterns=file_patterns,
            repo_path=args.repo_path,
            language=track["language"],
            config_path=args.config,
            force=args.force,
        )

    print(f"\nDone. {len(tracks)} track index(es) built.")


if __name__ == "__main__":
    main()
