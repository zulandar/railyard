#!/usr/bin/env bash
# Regenerate the Python gRPC stubs for the railyard plugin contract.
#
# This is DELIBERATELY standalone: it is NOT wired into railyard's
# buf.gen.yaml or scripts/proto.sh, and `go build ./...` / `go test ./...`
# never invoke it. The railyard repo does not depend on a Python toolchain;
# this example does, and only when you choose to (re)build it.
#
# Requirements (install into a venv — see README.md):
#   python -m pip install grpcio grpcio-tools
#
# Usage:
#   ./gen.sh            # regenerate stubs into ./railyard_plugin/
#
# The generated *_pb2.py / *_pb2_grpc.py files ARE committed so the example
# runs as-is. Re-run this only after copying a fresh proto/plugin.proto from
# pkg/plugin/proto/v1/plugin.proto (and bumping proto/grpc_broker.proto to
# match the pinned go-plugin version).

set -euo pipefail

cd "$(dirname "$0")"

OUT_DIR="railyard_plugin"
PROTO_DIR="proto"

PY="${PYTHON:-python}"

mkdir -p "$OUT_DIR"
touch "$OUT_DIR/__init__.py"

# grpc_tools bundles the well-known types (struct.proto, timestamp.proto),
# so no separate protobuf include path is needed for those imports.
#
# --proto_path="$PROTO_DIR" makes both plugin.proto and grpc_broker.proto
# resolvable. The generated modules import each other by their proto package
# path, so we emit them as a flat package under $OUT_DIR.
"$PY" -m grpc_tools.protoc \
  --proto_path="$PROTO_DIR" \
  --python_out="$OUT_DIR" \
  --grpc_python_out="$OUT_DIR" \
  "$PROTO_DIR/plugin.proto" \
  "$PROTO_DIR/grpc_broker.proto"

echo "Generated stubs in ./$OUT_DIR:"
ls -1 "$OUT_DIR"
