"""Unit tests for cocoindex/main.py — all cocoindex internals are mocked."""

import sys
import types
from unittest import mock

import pytest

# ---------------------------------------------------------------------------
# Stub numpy before anything imports main.py (it does `import numpy as np`)
# ---------------------------------------------------------------------------

_np = types.ModuleType("numpy")
_np.float32 = "float32"

_nptyping = types.ModuleType("numpy.typing")
_nptyping.NDArray = type("NDArray", (), {"__class_getitem__": classmethod(lambda cls, x: cls)})
_np.typing = _nptyping

sys.modules.setdefault("numpy", _np)
sys.modules.setdefault("numpy.typing", _nptyping)

# ---------------------------------------------------------------------------
# Stub out the cocoindex package before importing main.py so we never need
# the real native extension or a running Postgres.
# ---------------------------------------------------------------------------


def _build_cocoindex_stub():
    """Return a fake cocoindex module tree with the attributes main.py uses."""
    ci = types.ModuleType("cocoindex")

    # --- decorators ---
    def transform_flow():
        def decorator(fn):
            fn.transform = fn  # allow code_to_embedding.transform(...)
            return fn
        return decorator

    ci.transform_flow = transform_flow

    def flow_def(name=""):
        def decorator(fn):
            return fn
        return decorator

    ci.flow_def = flow_def

    # --- type stubs ---
    ci.FlowBuilder = type("FlowBuilder", (), {})
    ci.DataScope = type("DataScope", (), {})
    ci.DataSlice = type("DataSlice", (), {"__class_getitem__": classmethod(lambda cls, x: cls)})

    # --- cocoindex.functions ---
    functions = types.ModuleType("cocoindex.functions")
    functions.SplitRecursively = lambda: "SplitRecursively"
    functions.SentenceTransformerEmbed = lambda model="": f"SentenceTransformerEmbed({model})"
    ci.functions = functions

    # --- cocoindex.sources ---
    sources = types.ModuleType("cocoindex.sources")
    sources.LocalFile = lambda **kw: ("LocalFile", kw)
    ci.sources = sources

    # --- cocoindex.targets ---
    targets = types.ModuleType("cocoindex.targets")
    targets.Postgres = lambda **kw: ("Postgres", kw)
    ci.targets = targets

    # --- cocoindex.storages (alias some code paths reference) ---
    storages = types.ModuleType("cocoindex.storages")
    storages.Postgres = targets.Postgres
    ci.storages = storages

    # --- VectorIndexDef / VectorSimilarityMetric ---
    ci.VectorIndexDef = lambda **kw: ("VectorIndexDef", kw)

    class _Metric:
        COSINE_SIMILARITY = "cosine"

    ci.VectorSimilarityMetric = _Metric

    # --- runtime functions ---
    ci.init = mock.MagicMock()
    ci.open_flow = mock.MagicMock()
    ci.setup_all_flows = mock.MagicMock()

    return ci, functions, sources, targets, storages


_ci, _functions, _sources, _targets, _storages = _build_cocoindex_stub()

sys.modules["cocoindex"] = _ci
sys.modules["cocoindex.functions"] = _functions
sys.modules["cocoindex.sources"] = _sources
sys.modules["cocoindex.targets"] = _targets
sys.modules["cocoindex.storages"] = _storages

# Now safe to import main
from main import (  # noqa: E402
    EMBEDDING_MODEL,
    EXCLUDED_PATTERNS,
    LANGUAGE_MAP,
    code_to_embedding,
    make_flow_def,
    main,
    parse_args,
)


# ---------------------------------------------------------------------------
# Helper: build a mock FlowBuilder + DataScope that mirrors the CocoIndex
# context-manager protocol used by make_flow_def's inner function.
# ---------------------------------------------------------------------------


class _DictLikeMock:
    """A plain object with dict-like __getitem__/__setitem__ that avoids
    MagicMock's dunder descriptor issues."""

    def __init__(self, mapping=None):
        self._store = dict(mapping or {})
        self._sets = {}

    def __getitem__(self, key):
        if key in self._store:
            return self._store[key]
        m = mock.MagicMock(name=f"DictLikeMock[{key!r}]")
        self._store[key] = m
        return m

    def __setitem__(self, key, value):
        self._store[key] = value
        self._sets[key] = value


def _build_flow_mocks():
    """Return (flow_builder, data_scope, content_mock, collector) wired up
    so that calling the flow_def function exercises the full code path."""
    flow_builder = mock.MagicMock(name="flow_builder")

    # content_mock — stands in for doc["content"]
    content_mock = mock.MagicMock(name="content_mock")

    # chunks_mock — returned by content_mock.transform(...)
    chunks_mock = mock.MagicMock(name="chunks_mock")
    content_mock.transform.return_value = chunks_mock

    # chunk — yielded by chunks_mock.row() context manager
    chunk = _DictLikeMock()
    chunks_row_cm = mock.MagicMock()
    chunks_row_cm.__enter__ = mock.MagicMock(return_value=chunk)
    chunks_row_cm.__exit__ = mock.MagicMock(return_value=False)
    chunks_mock.row.return_value = chunks_row_cm

    # doc — yielded by data_scope["files"].row() context manager
    doc = _DictLikeMock({"content": content_mock, "chunks": chunks_mock})
    files_row_cm = mock.MagicMock()
    files_row_cm.__enter__ = mock.MagicMock(return_value=doc)
    files_row_cm.__exit__ = mock.MagicMock(return_value=False)

    files_mock = mock.MagicMock(name="files_mock")
    files_mock.row.return_value = files_row_cm

    # flow_builder.add_source() must return files_mock so the flow's
    # data_scope["files"] = flow_builder.add_source(...) stores our mock
    flow_builder.add_source.return_value = files_mock

    # collector
    collector = mock.MagicMock(name="collector")

    # data_scope
    data_scope = _DictLikeMock({"files": files_mock})
    data_scope.add_collector = mock.MagicMock(return_value=collector)

    return flow_builder, data_scope, content_mock, collector


# ===================================================================
# parse_args
# ===================================================================


class TestParseArgs:
    def test_required_track_and_patterns(self):
        args = parse_args(["--track", "backend", "--file-patterns", "*.go", "cmd/**"])
        assert args.track == "backend"
        assert args.file_patterns == ["*.go", "cmd/**"]

    def test_defaults(self):
        args = parse_args(["--track", "fe", "--file-patterns", "*.ts"])
        assert args.repo_path == "."
        assert args.language is None

    def test_all_flags(self):
        args = parse_args([
            "--track", "frontend",
            "--file-patterns", "src/**", "*.tsx",
            "--repo-path", "/repos/myapp",
            "--language", "typescript",
        ])
        assert args.track == "frontend"
        assert args.file_patterns == ["src/**", "*.tsx"]
        assert args.repo_path == "/repos/myapp"
        assert args.language == "typescript"

    def test_missing_track_exits(self):
        with pytest.raises(SystemExit):
            parse_args(["--file-patterns", "*.go"])

    def test_missing_file_patterns_exits(self):
        with pytest.raises(SystemExit):
            parse_args(["--track", "backend"])


# ===================================================================
# make_flow_def
# ===================================================================


class TestMakeFlowDef:
    def test_returns_callable(self):
        fn = make_flow_def("backend", ["*.go"], "/repos/app")
        assert callable(fn)

    def test_table_name_format(self):
        """The flow should export to main_{track}_embeddings."""
        flow_builder, data_scope, content_mock, collector = _build_flow_mocks()

        fn = make_flow_def("backend", ["*.go", "cmd/**"], "/repos/app", language="go")
        fn(flow_builder, data_scope)

        # Verify the source was created with correct patterns
        flow_builder.add_source.assert_called_once()
        source_arg = flow_builder.add_source.call_args[0][0]
        assert source_arg == ("LocalFile", {
            "path": "/repos/app",
            "included_patterns": ["*.go", "cmd/**"],
            "excluded_patterns": EXCLUDED_PATTERNS,
        })

        # Verify export was called with correct table name
        collector.export.assert_called_once()
        export_call = collector.export.call_args
        assert export_call[0][0] == "code_embeddings"
        assert export_call[0][1] == ("Postgres", {"table_name": "main_backend_embeddings"})

    def test_table_name_varies_by_track(self):
        """Different track names produce different table names."""
        flow_builder, data_scope, _, collector = _build_flow_mocks()

        fn = make_flow_def("frontend", ["*.ts"], "/repos/app", language="typescript")
        fn(flow_builder, data_scope)

        export_call = collector.export.call_args
        assert export_call[0][1] == ("Postgres", {"table_name": "main_frontend_embeddings"})

    def test_language_none_omits_treesitter(self):
        """When language is None, SplitRecursively should not get a language kwarg."""
        flow_builder, data_scope, content_mock, _ = _build_flow_mocks()

        fn = make_flow_def("mixed_track", ["*"], "/repos/app", language=None)
        fn(flow_builder, data_scope)

        transform_call = content_mock.transform.call_args
        assert "language" not in transform_call[1]
        assert transform_call[1]["chunk_size"] == 1500
        assert transform_call[1]["chunk_overlap"] == 300

    def test_language_go_includes_treesitter(self):
        """When language='go', SplitRecursively should get language='go'."""
        flow_builder, data_scope, content_mock, _ = _build_flow_mocks()

        fn = make_flow_def("backend", ["*.go"], "/repos/app", language="go")
        fn(flow_builder, data_scope)

        transform_call = content_mock.transform.call_args
        assert transform_call[1]["language"] == "go"

    def test_language_typescript_includes_treesitter(self):
        flow_builder, data_scope, content_mock, _ = _build_flow_mocks()

        fn = make_flow_def("frontend", ["*.ts"], "/repos/app", language="typescript")
        fn(flow_builder, data_scope)

        transform_call = content_mock.transform.call_args
        assert transform_call[1]["language"] == "typescript"

    def test_mixed_language_no_treesitter(self):
        """LANGUAGE_MAP['mixed'] is None, so no tree-sitter language."""
        flow_builder, data_scope, content_mock, _ = _build_flow_mocks()

        fn = make_flow_def("infra", ["Makefile"], "/repos/app", language="mixed")
        fn(flow_builder, data_scope)

        transform_call = content_mock.transform.call_args
        assert "language" not in transform_call[1]

    def test_unknown_language_no_treesitter(self):
        """An unrecognized language should not pass a tree-sitter language."""
        flow_builder, data_scope, content_mock, _ = _build_flow_mocks()

        fn = make_flow_def("exotic", ["*.zig"], "/repos/app", language="zig")
        fn(flow_builder, data_scope)

        transform_call = content_mock.transform.call_args
        assert "language" not in transform_call[1]

    def test_chunk_params(self):
        """Chunk size=1500, overlap=300 per architecture spec."""
        flow_builder, data_scope, content_mock, _ = _build_flow_mocks()

        fn = make_flow_def("backend", ["*.go"], "/repos/app", language="go")
        fn(flow_builder, data_scope)

        transform_call = content_mock.transform.call_args
        assert transform_call[1]["chunk_size"] == 1500
        assert transform_call[1]["chunk_overlap"] == 300

    def test_excluded_patterns_passed(self):
        """Excluded patterns from EXCLUDED_PATTERNS are forwarded to LocalFile."""
        flow_builder, data_scope, _, _ = _build_flow_mocks()

        fn = make_flow_def("backend", ["*.go"], "/repos/app")
        fn(flow_builder, data_scope)

        source_arg = flow_builder.add_source.call_args[0][0]
        assert source_arg[1]["excluded_patterns"] == EXCLUDED_PATTERNS

    def test_vector_index_cosine(self):
        """Export should include a cosine similarity vector index on 'embedding'."""
        flow_builder, data_scope, _, collector = _build_flow_mocks()

        fn = make_flow_def("backend", ["*.go"], "/repos/app")
        fn(flow_builder, data_scope)

        export_call = collector.export.call_args
        vector_indexes = export_call[1]["vector_indexes"]
        assert len(vector_indexes) == 1
        assert vector_indexes[0] == ("VectorIndexDef", {
            "field_name": "embedding",
            "metric": "cosine",
        })

    def test_primary_key_fields(self):
        """Primary key should be (filename, location)."""
        flow_builder, data_scope, _, collector = _build_flow_mocks()

        fn = make_flow_def("backend", ["*.go"], "/repos/app")
        fn(flow_builder, data_scope)

        export_call = collector.export.call_args
        assert export_call[1]["primary_key_fields"] == ["filename", "location"]


# ===================================================================
# main()
# ===================================================================


class TestMain:
    def test_calls_cocoindex_lifecycle(self):
        _ci.init.reset_mock()
        _ci.open_flow.reset_mock()
        _ci.setup_all_flows.reset_mock()

        flow_mock = mock.MagicMock()
        _ci.open_flow.return_value = flow_mock

        main(["--track", "backend", "--file-patterns", "*.go", "--repo-path", "/tmp/repo"])

        _ci.init.assert_called_once()
        _ci.open_flow.assert_called_once()
        assert _ci.open_flow.call_args[0][0] == "CodeEmbedding_backend"
        _ci.setup_all_flows.assert_called_once()
        flow_mock.update.assert_called_once()

    def test_flow_name_includes_track(self):
        _ci.open_flow.reset_mock()
        _ci.open_flow.return_value = mock.MagicMock()

        main(["--track", "frontend", "--file-patterns", "*.ts"])

        assert _ci.open_flow.call_args[0][0] == "CodeEmbedding_frontend"

    def test_flow_def_passed_to_open_flow(self):
        """The second arg to open_flow should be callable (the flow_def)."""
        _ci.open_flow.reset_mock()
        _ci.open_flow.return_value = mock.MagicMock()

        main(["--track", "api", "--file-patterns", "*.py", "--language", "python"])

        flow_def_arg = _ci.open_flow.call_args[0][1]
        assert callable(flow_def_arg)


# ===================================================================
# Constants / module-level
# ===================================================================


class TestConstants:
    def test_embedding_model(self):
        assert EMBEDDING_MODEL == "sentence-transformers/all-MiniLM-L6-v2"

    def test_excluded_patterns_contains_required(self):
        for pat in [".*", "vendor", "node_modules", ".git"]:
            assert pat in EXCLUDED_PATTERNS

    def test_language_map_go(self):
        assert LANGUAGE_MAP["go"] == "go"

    def test_language_map_typescript(self):
        assert LANGUAGE_MAP["typescript"] == "typescript"

    def test_language_map_python(self):
        assert LANGUAGE_MAP["python"] == "python"

    def test_language_map_mixed_is_none(self):
        assert LANGUAGE_MAP["mixed"] is None

    def test_code_to_embedding_is_callable(self):
        assert callable(code_to_embedding)
