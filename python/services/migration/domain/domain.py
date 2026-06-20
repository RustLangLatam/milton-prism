"""Migration domain types — proto aliases (A.3)."""
from __future__ import annotations

from milton_prism.types.migration.v1.migration_pb2 import (
    Migration,
    MigrationsFilter,
    MigrationState,
    TargetConfig,
    TargetDatabase,
    TargetLanguage,
)
from milton_prism.types.pagination.v1.pagination_pb2 import Pagination
from milton_prism.types.query_params.v1.query_params_pb2 import PageQueryParams

MigrationStateUnspecified = MigrationState.MIGRATION_STATE_UNSPECIFIED
MigrationStatePending = MigrationState.MIGRATION_STATE_PENDING
MigrationStateAnalyzing = MigrationState.MIGRATION_STATE_ANALYZING
MigrationStateDesigning = MigrationState.MIGRATION_STATE_DESIGNING
MigrationStateAwaitingApproval = MigrationState.MIGRATION_STATE_AWAITING_APPROVAL
MigrationStateGenerating = MigrationState.MIGRATION_STATE_GENERATING
MigrationStateTesting = MigrationState.MIGRATION_STATE_TESTING
MigrationStateReady = MigrationState.MIGRATION_STATE_READY
MigrationStatePushed = MigrationState.MIGRATION_STATE_PUSHED
MigrationStateFailed = MigrationState.MIGRATION_STATE_FAILED
MigrationStateCancelled = MigrationState.MIGRATION_STATE_CANCELLED

TargetLanguageUnspecified = TargetLanguage.TARGET_LANGUAGE_UNSPECIFIED
TargetDatabaseUnspecified = TargetDatabase.TARGET_DATABASE_UNSPECIFIED

__all__ = [
    "Migration",
    "MigrationState",
    "MigrationStateAnalyzing",
    "MigrationStateAwaitingApproval",
    "MigrationStateCancelled",
    "MigrationStateDesigning",
    "MigrationStateFailed",
    "MigrationStateGenerating",
    "MigrationStatePending",
    "MigrationStatePushed",
    "MigrationStateReady",
    "MigrationStateTesting",
    "MigrationStateUnspecified",
    "MigrationsFilter",
    "PageQueryParams",
    "Pagination",
    "TargetConfig",
    "TargetDatabase",
    "TargetDatabaseUnspecified",
    "TargetLanguage",
    "TargetLanguageUnspecified",
]
