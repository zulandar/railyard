"""Unit tests for cocoindex/build_all.py."""

import os
import tempfile
from unittest import mock

import pytest
import yaml

from build_all import load_tracks, main, parse_args


# ===================================================================
# load_tracks
# ===================================================================


class TestLoadTracks:
    def _write_yaml(self, data):
        f = tempfile.NamedTemporaryFile(
            mode="w", suffix=".yaml", delete=False,
        )
        yaml.dump(data, f)
        f.close()
        return f.name

    def test_loads_single_track(self):
        path = self._write_yaml({
            "tracks": [
                {"name": "backend", "language": "go", "file_patterns": ["*.go"]},
            ]
        })
        try:
            tracks = load_tracks(path)
            assert len(tracks) == 1
            assert tracks[0]["name"] == "backend"
            assert tracks[0]["language"] == "go"
            assert tracks[0]["file_patterns"] == ["*.go"]
        finally:
            os.unlink(path)

    def test_loads_multiple_tracks(self):
        path = self._write_yaml({
            "tracks": [
                {"name": "backend", "language": "go", "file_patterns": ["*.go"]},
                {"name": "frontend", "language": "typescript", "file_patterns": ["*.ts"]},
            ]
        })
        try:
            tracks = load_tracks(path)
            assert len(tracks) == 2
            assert tracks[0]["name"] == "backend"
            assert tracks[1]["name"] == "frontend"
        finally:
            os.unlink(path)

    def test_empty_tracks(self):
        path = self._write_yaml({"tracks": []})
        try:
            assert load_tracks(path) == []
        finally:
            os.unlink(path)

    def test_no_tracks_key(self):
        path = self._write_yaml({"owner": "testuser"})
        try:
            assert load_tracks(path) == []
        finally:
            os.unlink(path)

    def test_skips_tracks_without_name(self):
        path = self._write_yaml({
            "tracks": [
                {"language": "go", "file_patterns": ["*.go"]},
                {"name": "backend", "language": "go", "file_patterns": ["*.go"]},
            ]
        })
        try:
            tracks = load_tracks(path)
            assert len(tracks) == 1
            assert tracks[0]["name"] == "backend"
        finally:
            os.unlink(path)

    def test_default_file_patterns(self):
        path = self._write_yaml({
            "tracks": [{"name": "backend", "language": "go"}]
        })
        try:
            tracks = load_tracks(path)
            assert tracks[0]["file_patterns"] == []
        finally:
            os.unlink(path)

    def test_language_can_be_none(self):
        path = self._write_yaml({
            "tracks": [{"name": "misc", "file_patterns": ["*"]}]
        })
        try:
            tracks = load_tracks(path)
            assert tracks[0]["language"] is None
        finally:
            os.unlink(path)


# ===================================================================
# parse_args
# ===================================================================


class TestParseArgs:
    def test_required_railyard_config(self):
        args = parse_args(["--railyard-config", "railyard.yaml"])
        assert args.railyard_config == "railyard.yaml"

    def test_defaults(self):
        args = parse_args(["--railyard-config", "r.yaml"])
        assert args.repo_path == "."
        assert args.config is None
        assert args.tracks is None

    def test_all_flags(self):
        args = parse_args([
            "--railyard-config", "railyard.yaml",
            "--repo-path", "/repos/app",
            "--config", "cocoindex.yaml",
            "--tracks", "backend", "frontend",
        ])
        assert args.railyard_config == "railyard.yaml"
        assert args.repo_path == "/repos/app"
        assert args.config == "cocoindex.yaml"
        assert args.tracks == ["backend", "frontend"]

    def test_missing_railyard_config_exits(self):
        with pytest.raises(SystemExit):
            parse_args([])


# ===================================================================
# main
# ===================================================================


class TestMain:
    def _write_yaml(self, data):
        f = tempfile.NamedTemporaryFile(
            mode="w", suffix=".yaml", delete=False,
        )
        yaml.dump(data, f)
        f.close()
        return f.name

    @mock.patch("build_all.build_track")
    def test_builds_all_tracks(self, mock_build):
        path = self._write_yaml({
            "tracks": [
                {"name": "backend", "language": "go", "file_patterns": ["*.go", "cmd/**"]},
                {"name": "frontend", "language": "typescript", "file_patterns": ["*.ts"]},
            ]
        })
        try:
            main(["--railyard-config", path])
            assert mock_build.call_count == 2
            calls = mock_build.call_args_list
            assert calls[0][1]["track_name"] == "backend"
            assert calls[1][1]["track_name"] == "frontend"
        finally:
            os.unlink(path)

    @mock.patch("build_all.build_track")
    def test_filters_by_tracks_flag(self, mock_build):
        path = self._write_yaml({
            "tracks": [
                {"name": "backend", "language": "go", "file_patterns": ["*.go"]},
                {"name": "frontend", "language": "typescript", "file_patterns": ["*.ts"]},
            ]
        })
        try:
            main(["--railyard-config", path, "--tracks", "backend"])
            assert mock_build.call_count == 1
            assert mock_build.call_args[1]["track_name"] == "backend"
        finally:
            os.unlink(path)

    @mock.patch("build_all.build_track")
    def test_passes_repo_path(self, mock_build):
        path = self._write_yaml({
            "tracks": [{"name": "be", "language": "go", "file_patterns": ["*.go"]}]
        })
        try:
            main(["--railyard-config", path, "--repo-path", "/repos/app"])
            assert mock_build.call_args[1]["repo_path"] == "/repos/app"
        finally:
            os.unlink(path)

    @mock.patch("build_all.build_track")
    def test_passes_language(self, mock_build):
        path = self._write_yaml({
            "tracks": [{"name": "be", "language": "go", "file_patterns": ["*.go"]}]
        })
        try:
            main(["--railyard-config", path])
            assert mock_build.call_args[1]["language"] == "go"
        finally:
            os.unlink(path)

    @mock.patch("build_all.build_track")
    def test_passes_file_patterns(self, mock_build):
        path = self._write_yaml({
            "tracks": [
                {"name": "be", "language": "go", "file_patterns": ["*.go", "cmd/**"]},
            ]
        })
        try:
            main(["--railyard-config", path])
            assert mock_build.call_args[1]["file_patterns"] == ["*.go", "cmd/**"]
        finally:
            os.unlink(path)

    def test_exits_when_no_tracks(self):
        path = self._write_yaml({"tracks": []})
        try:
            with pytest.raises(SystemExit):
                main(["--railyard-config", path])
        finally:
            os.unlink(path)

    @mock.patch("build_all.build_track")
    def test_exits_when_filter_matches_nothing(self, mock_build):
        path = self._write_yaml({
            "tracks": [{"name": "backend", "language": "go", "file_patterns": ["*.go"]}]
        })
        try:
            with pytest.raises(SystemExit):
                main(["--railyard-config", path, "--tracks", "nonexistent"])
        finally:
            os.unlink(path)
