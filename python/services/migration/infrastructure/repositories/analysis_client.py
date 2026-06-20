"""NoOpAnalysisClient — intentionally noisy stub (A.3).

Python deviation: raises DomainError instead of returning None like the Go stub.
The analysis service does not exist in v1. Raising prevents false positives during
profile validation — an unimplemented port that silently succeeds is misleading.
"""
from __future__ import annotations

from shared.errors import DomainError


class NoOpAnalysisClient:
    async def validate_analysis_summary_exists(self, summary_id: int) -> None:
        raise DomainError("MIG500", "Failure_Analysis_Not_Implemented")  # TODO
