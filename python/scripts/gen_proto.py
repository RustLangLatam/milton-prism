#!/usr/bin/env python3
"""Generate Python gRPC stubs from the proto contracts shared with the Go side.

Run from python/ after poetry install:
    poetry run python scripts/gen_proto.py

Output goes to gen/ which is never edited by hand (A.9).
"""
from __future__ import annotations

import os
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
PROTO_ROOT = REPO_ROOT / "protobuf" / "proto"
PYTHON_ROOT = Path(__file__).resolve().parent.parent
OUT_DIR = PYTHON_ROOT / "gen"

PROTO_FILES = [
    # openapiv3 annotations — imported by all service/type protos
    "openapiv3/OpenAPIv3.proto",
    "openapiv3/annotations.proto",
    # Milton Prism types
    "milton_prism/types/identity/v1/user.proto",
    "milton_prism/types/token/v1/token.proto",
    "milton_prism/types/pagination/v1/pagination.proto",
    "milton_prism/types/query_params/v1/query_params.proto",
    # Milton Prism type contracts
    "milton_prism/types/repository/v1/repository.proto",
    "milton_prism/types/migration/v1/migration.proto",
    "milton_prism/types/analysis/v1/analysis.proto",
    # Milton Prism service contracts
    "milton_prism/services/identity/v1/identity_service.proto",
    "milton_prism/services/repository/v1/repository_service.proto",
    "milton_prism/services/migration/v1/migration_service.proto",
]

# Buf lock commit for buf.build/googleapis/googleapis (from protobuf/buf.lock).
_GOOGLEAPIS_COMMIT = "c17df5b2beca46928cc87d5656bd5343"
_BUF_CACHE_ROOT = Path.home() / ".cache" / "buf" / "v3" / "modules" / "b5"
_GOOGLEAPIS_CACHE = (
    _BUF_CACHE_ROOT / "buf.build" / "googleapis" / "googleapis" / _GOOGLEAPIS_COMMIT / "files"
)


def _grpcio_tools_proto_path() -> Path:
    import grpc_tools  # noqa: PLC0415

    return Path(grpc_tools.__file__).parent / "_proto"


def _add_init_files(base: Path) -> None:
    """Ensure every generated package directory has an __init__.py."""
    for dirpath, _dirnames, _filenames in os.walk(base):
        d = Path(dirpath)
        if any(d.glob("*_pb2*.py")):
            init = d / "__init__.py"
            if not init.exists():
                init.write_text("# generated — do not edit\n")
            # also walk up to ensure parent packages exist
            p = d.parent
            while p != base.parent:
                parent_init = p / "__init__.py"
                if not parent_init.exists():
                    parent_init.write_text("# generated — do not edit\n")
                p = p.parent


def main() -> int:
    OUT_DIR.mkdir(parents=True, exist_ok=True)

    from grpc_tools import protoc  # noqa: PLC0415

    grpcio_proto = _grpcio_tools_proto_path()
    if not _GOOGLEAPIS_CACHE.exists():
        print(
            f"ERROR: googleapis buf cache not found at {_GOOGLEAPIS_CACHE}\n"
            "Run 'buf dep update' from protobuf/ to populate the cache.",
            file=sys.stderr,
        )
        return 1

    args = [
        "grpc_tools.protoc",
        f"-I{PROTO_ROOT}",
        f"-I{grpcio_proto}",
        f"-I{_GOOGLEAPIS_CACHE}",
        f"--python_out={OUT_DIR}",
        f"--pyi_out={OUT_DIR}",
        f"--grpc_python_out={OUT_DIR}",
        *(str(PROTO_ROOT / f) for f in PROTO_FILES),
    ]

    rc = protoc.main(args)
    if rc == 0:
        _add_init_files(OUT_DIR)
        print("Proto stubs generated successfully.", file=sys.stderr)
    return rc  # type: ignore[no-any-return]


if __name__ == "__main__":
    sys.exit(main())
