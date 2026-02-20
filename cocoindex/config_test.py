"""Unit tests for cocoindex/config.py."""

import os
import tempfile
from pathlib import Path

import pytest
import yaml

from config import (
    DEFAULT_EXCLUDED_PATTERNS,
    DEFAULT_MAIN_TABLE_TEMPLATE,
    DEFAULT_OVERLAY_TABLE_PREFIX,
    CocoIndexConfig,
    TrackOverrides,
    load_config,
)


# ===================================================================
# CocoIndexConfig defaults
# ===================================================================


class TestCocoIndexConfigDefaults:
    def test_default_main_table_template(self):
        cfg = CocoIndexConfig()
        assert cfg.main_table_template == "main_{track}_embeddings"

    def test_default_overlay_table_prefix(self):
        cfg = CocoIndexConfig()
        assert cfg.overlay_table_prefix == "ovl_"

    def test_default_excluded_patterns(self):
        cfg = CocoIndexConfig()
        assert cfg.excluded_patterns == DEFAULT_EXCLUDED_PATTERNS

    def test_default_tracks_empty(self):
        cfg = CocoIndexConfig()
        assert cfg.tracks == {}


# ===================================================================
# main_table_name
# ===================================================================


class TestMainTableName:
    def test_default_template(self):
        cfg = CocoIndexConfig()
        assert cfg.main_table_name("backend") == "main_backend_embeddings"

    def test_custom_template(self):
        cfg = CocoIndexConfig(main_table_template="idx_{track}")
        assert cfg.main_table_name("frontend") == "idx_frontend"

    def test_different_tracks(self):
        cfg = CocoIndexConfig()
        assert cfg.main_table_name("backend") == "main_backend_embeddings"
        assert cfg.main_table_name("frontend") == "main_frontend_embeddings"
        assert cfg.main_table_name("infra") == "main_infra_embeddings"


# ===================================================================
# overlay_table_name
# ===================================================================


class TestOverlayTableName:
    def test_default_prefix(self):
        cfg = CocoIndexConfig()
        assert cfg.overlay_table_name("eng-abc123") == "ovl_eng_abc123"

    def test_custom_prefix(self):
        cfg = CocoIndexConfig(overlay_table_prefix="overlay_")
        assert cfg.overlay_table_name("eng-abc123") == "overlay_eng_abc123"

    def test_rejects_invalid_engine_id(self):
        cfg = CocoIndexConfig()
        with pytest.raises(ValueError, match="invalid engine_id"):
            cfg.overlay_table_name("eng; DROP TABLE")

    def test_rejects_empty_engine_id(self):
        cfg = CocoIndexConfig()
        with pytest.raises(ValueError, match="invalid engine_id"):
            cfg.overlay_table_name("")


# ===================================================================
# excluded_patterns_for_track
# ===================================================================


class TestExcludedPatternsForTrack:
    def test_returns_default_when_no_override(self):
        cfg = CocoIndexConfig()
        assert cfg.excluded_patterns_for_track("backend") == DEFAULT_EXCLUDED_PATTERNS

    def test_returns_override_when_set(self):
        cfg = CocoIndexConfig(
            tracks={"backend": TrackOverrides(excluded_patterns=[".*", ".git"])}
        )
        assert cfg.excluded_patterns_for_track("backend") == [".*", ".git"]

    def test_returns_default_for_unspecified_track(self):
        cfg = CocoIndexConfig(
            tracks={"backend": TrackOverrides(excluded_patterns=[".*"])}
        )
        assert cfg.excluded_patterns_for_track("frontend") == DEFAULT_EXCLUDED_PATTERNS

    def test_none_override_uses_default(self):
        cfg = CocoIndexConfig(
            tracks={"backend": TrackOverrides(excluded_patterns=None)}
        )
        assert cfg.excluded_patterns_for_track("backend") == DEFAULT_EXCLUDED_PATTERNS


# ===================================================================
# included_patterns_for_track
# ===================================================================


class TestIncludedPatternsForTrack:
    def test_returns_default_when_no_override(self):
        cfg = CocoIndexConfig()
        default = ["*.go", "cmd/**"]
        assert cfg.included_patterns_for_track("backend", default) == default

    def test_returns_override_when_set(self):
        override_patterns = ["src/**", "*.ts"]
        cfg = CocoIndexConfig(
            tracks={"frontend": TrackOverrides(included_patterns=override_patterns)}
        )
        assert cfg.included_patterns_for_track("frontend", ["*.tsx"]) == override_patterns

    def test_returns_default_for_unspecified_track(self):
        cfg = CocoIndexConfig(
            tracks={"backend": TrackOverrides(included_patterns=["*.go"])}
        )
        default = ["*.ts"]
        assert cfg.included_patterns_for_track("frontend", default) == default

    def test_none_override_uses_default(self):
        cfg = CocoIndexConfig(
            tracks={"backend": TrackOverrides(included_patterns=None)}
        )
        default = ["*.go"]
        assert cfg.included_patterns_for_track("backend", default) == default


# ===================================================================
# load_config — file loading
# ===================================================================


class TestLoadConfig:
    def test_returns_defaults_when_no_file(self):
        cfg = load_config("/nonexistent/path/cocoindex.yaml")
        assert cfg.main_table_template == DEFAULT_MAIN_TABLE_TEMPLATE
        assert cfg.overlay_table_prefix == DEFAULT_OVERLAY_TABLE_PREFIX
        assert cfg.excluded_patterns == list(DEFAULT_EXCLUDED_PATTERNS)

    def test_loads_from_explicit_path(self):
        data = {
            "main_table_template": "custom_{track}_idx",
            "overlay_table_prefix": "ovr_",
            "excluded_patterns": [".*", ".git"],
        }
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".yaml", delete=False,
        ) as f:
            yaml.dump(data, f)
            path = f.name
        try:
            cfg = load_config(path)
            assert cfg.main_table_template == "custom_{track}_idx"
            assert cfg.overlay_table_prefix == "ovr_"
            assert cfg.excluded_patterns == [".*", ".git"]
        finally:
            os.unlink(path)

    def test_loads_per_track_overrides(self):
        data = {
            "tracks": {
                "backend": {
                    "excluded_patterns": [".*", "vendor"],
                },
                "frontend": {
                    "included_patterns": ["src/**"],
                    "excluded_patterns": [".*", "node_modules"],
                },
            }
        }
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".yaml", delete=False,
        ) as f:
            yaml.dump(data, f)
            path = f.name
        try:
            cfg = load_config(path)
            assert cfg.excluded_patterns_for_track("backend") == [".*", "vendor"]
            assert cfg.included_patterns_for_track("frontend", []) == ["src/**"]
            assert cfg.excluded_patterns_for_track("frontend") == [".*", "node_modules"]
        finally:
            os.unlink(path)

    def test_empty_yaml_returns_defaults(self):
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".yaml", delete=False,
        ) as f:
            f.write("")
            path = f.name
        try:
            cfg = load_config(path)
            assert cfg.main_table_template == DEFAULT_MAIN_TABLE_TEMPLATE
        finally:
            os.unlink(path)

    def test_partial_yaml_fills_defaults(self):
        data = {"overlay_table_prefix": "custom_"}
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".yaml", delete=False,
        ) as f:
            yaml.dump(data, f)
            path = f.name
        try:
            cfg = load_config(path)
            assert cfg.overlay_table_prefix == "custom_"
            assert cfg.main_table_template == DEFAULT_MAIN_TABLE_TEMPLATE
        finally:
            os.unlink(path)

    def test_auto_detection_from_none(self):
        """load_config(None) should auto-detect cocoindex.yaml near the module."""
        cfg = load_config(None)
        # Should find the cocoindex.yaml we created in the cocoindex/ directory
        assert cfg.main_table_template == DEFAULT_MAIN_TABLE_TEMPLATE
