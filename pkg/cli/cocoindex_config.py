"""
Configuration loader for CocoIndex per-track settings.

Reads cocoindex.yaml for table naming templates, default exclusion patterns,
and optional per-track overrides. Falls back to sensible defaults when no
config file is present.
"""

import os
from dataclasses import dataclass, field
from pathlib import Path

import yaml

# ---------------------------------------------------------------------------
# Defaults — match the hardcoded values in main.py / overlay.py
# ---------------------------------------------------------------------------

DEFAULT_MAIN_TABLE_TEMPLATE = "main_{track}_embeddings"
DEFAULT_OVERLAY_TABLE_PREFIX = "ovl_"
DEFAULT_EXCLUDED_PATTERNS = [
    ".*", "vendor", "node_modules", "dist", "__pycache__", ".git",
]

CONFIG_FILENAME = "cocoindex.yaml"


# ---------------------------------------------------------------------------
# Data classes
# ---------------------------------------------------------------------------

@dataclass
class TrackOverrides:
    """Per-track overrides for included/excluded patterns."""
    included_patterns: list[str] | None = None
    excluded_patterns: list[str] | None = None


@dataclass
class CocoIndexConfig:
    """Loaded CocoIndex configuration."""
    main_table_template: str = DEFAULT_MAIN_TABLE_TEMPLATE
    overlay_table_prefix: str = DEFAULT_OVERLAY_TABLE_PREFIX
    excluded_patterns: list[str] = field(default_factory=lambda: list(DEFAULT_EXCLUDED_PATTERNS))
    tracks: dict[str, TrackOverrides] = field(default_factory=dict)

    def main_table_name(self, track: str) -> str:
        """Resolve main table name for a track."""
        return self.main_table_template.replace("{track}", track)

    def overlay_table_name(self, engine_id: str) -> str:
        """Resolve overlay table name for an engine ID.

        Sanitizes the engine_id (replaces hyphens with underscores) and
        prepends the configured overlay table prefix.
        """
        import re
        safe_re = re.compile(r"^[a-zA-Z0-9_-]+$")
        if not safe_re.match(engine_id):
            raise ValueError(f"invalid engine_id: {engine_id!r}")
        return self.overlay_table_prefix + engine_id.replace("-", "_")

    def excluded_patterns_for_track(self, track: str) -> list[str]:
        """Return excluded patterns for a track (per-track override or default)."""
        override = self.tracks.get(track)
        if override and override.excluded_patterns is not None:
            return override.excluded_patterns
        return self.excluded_patterns

    def included_patterns_for_track(
        self, track: str, default_patterns: list[str],
    ) -> list[str]:
        """Return included patterns for a track.

        Uses per-track override if set, otherwise falls back to
        default_patterns (typically from railyard.yaml file_patterns).
        """
        override = self.tracks.get(track)
        if override and override.included_patterns is not None:
            return override.included_patterns
        return default_patterns


# ---------------------------------------------------------------------------
# Loader
# ---------------------------------------------------------------------------


def _parse_track_overrides(raw: dict) -> dict[str, TrackOverrides]:
    """Parse the tracks section of the config."""
    result = {}
    for track_name, track_data in raw.items():
        if not isinstance(track_data, dict):
            continue
        result[track_name] = TrackOverrides(
            included_patterns=track_data.get("included_patterns"),
            excluded_patterns=track_data.get("excluded_patterns"),
        )
    return result


def load_config(config_path: str | Path | None = None) -> CocoIndexConfig:
    """Load CocoIndex config from a YAML file.

    Search order when config_path is None:
    1. ./cocoindex.yaml  (next to this script)
    2. ../cocoindex.yaml (repo root when running from cocoindex/)

    Returns default config if no file is found.
    """
    if config_path is not None:
        path = Path(config_path)
    else:
        # Search relative to this module's directory
        module_dir = Path(os.path.dirname(os.path.abspath(__file__)))
        candidates = [
            module_dir / CONFIG_FILENAME,
            module_dir.parent / CONFIG_FILENAME,
        ]
        path = None
        for candidate in candidates:
            if candidate.is_file():
                path = candidate
                break

    if path is None or not path.is_file():
        return CocoIndexConfig()

    with open(path, encoding="utf-8") as f:
        raw = yaml.safe_load(f) or {}

    tracks_raw = raw.get("tracks") or {}

    return CocoIndexConfig(
        main_table_template=raw.get("main_table_template", DEFAULT_MAIN_TABLE_TEMPLATE),
        overlay_table_prefix=raw.get("overlay_table_prefix", DEFAULT_OVERLAY_TABLE_PREFIX),
        excluded_patterns=raw.get("excluded_patterns", list(DEFAULT_EXCLUDED_PATTERNS)),
        tracks=_parse_track_overrides(tracks_raw),
    )
