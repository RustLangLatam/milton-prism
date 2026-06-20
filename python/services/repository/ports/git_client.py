"""GitClient port — git remote operations (A.3)."""
from __future__ import annotations

from typing import Protocol, runtime_checkable

from services.repository.domain.domain import Branch, ConnectionStatus


@runtime_checkable
class GitClient(Protocol):
    """Driven port for git remote operations. NoOpGitClient implements this (TODO)."""

    async def test_connection(
        self, remote_url: str, credential_ref: str
    ) -> ConnectionStatus:
        """Probe remoteURL using credentialRef and return the connection status."""
        ...

    async def list_branches(
        self, remote_url: str, credential_ref: str
    ) -> list[Branch]:
        """Return the branches available on the remote."""
        ...

    async def push_result(
        self,
        remote_url: str,
        credential_ref: str,
        target_branch: str,
        create_new_repo: bool,
    ) -> tuple[str, str]:
        """Push migration output.  Returns (pushed_branch, new_repo_url)."""
        ...
