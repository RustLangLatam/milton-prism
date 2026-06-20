"""Root conftest: ensure gen/ is on sys.path so proto stubs are importable."""
from __future__ import annotations

import sys
from pathlib import Path

_GEN = Path(__file__).parent / "gen"
if str(_GEN) not in sys.path:
    sys.path.insert(0, str(_GEN))
