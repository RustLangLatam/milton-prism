"""AnalysisClient port (A.3)."""
from __future__ import annotations

from typing import Protocol, runtime_checkable


@runtime_checkable
class AnalysisClient(Protocol):
    async def validate_analysis_summary_exists(self, summary_id: int) -> None: ...
