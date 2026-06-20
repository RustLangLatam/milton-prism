"""NoOpGitClient — stub for the GitClient port (A.3).

TODO: Replace with a real git client (e.g. pygit2, dulwich) once the git
infrastructure is provisioned.  This stub raises DomainError so that callers
cannot accidentally succeed against unimplemented git operations.

Intentional deviation from Go's NoOpGitClient (which returns success silently):
Python stubs are noisy to prevent false positives during profile validation.
Documented in docs/prism/python-parity-report.md.
"""
from __future__ import annotations

from services.repository.domain.domain import Branch, ConnectionStatus
from shared.errors import DomainError

_NOT_IMPL = DomainError("REPO500", "Failure_Git_Not_Implemented")


class NoOpGitClient:
    """Stub GitClient — all methods raise DomainError(REPO500). TODO: implement."""

    async def test_connection(
        self, remote_url: str, credential_ref: str
    ) -> ConnectionStatus:
        # TODO: implement real git connectivity probe
        raise DomainError(_NOT_IMPL.code, _NOT_IMPL.message)

    async def list_branches(
        self, remote_url: str, credential_ref: str
    ) -> list[Branch]:
        # TODO: implement real branch listing via remote git
        raise DomainError(_NOT_IMPL.code, _NOT_IMPL.message)

    async def push_result(
        self,
        remote_url: str,
        credential_ref: str,
        target_branch: str,
        create_new_repo: bool,
    ) -> tuple[str, str]:
        # TODO: implement real push to remote git
        raise DomainError(_NOT_IMPL.code, _NOT_IMPL.message)
