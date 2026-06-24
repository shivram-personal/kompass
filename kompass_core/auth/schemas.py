"""Pydantic request/response models for the auth + admin APIs.

`extra="forbid"` rejects unknown fields (SPEC §8 input validation). Responses
never carry password hashes or any secret.
"""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict, Field

from ..models import User


class _Strict(BaseModel):
    model_config = ConfigDict(extra="forbid")


class LoginRequest(_Strict):
    username: str = Field(min_length=1, max_length=255)
    password: str = Field(min_length=1, max_length=1024)


class ChangePasswordRequest(_Strict):
    current_password: str = Field(min_length=1, max_length=1024)
    new_password: str = Field(min_length=1, max_length=1024)


class CreateUserRequest(_Strict):
    username: str = Field(min_length=1, max_length=255)
    password: str = Field(min_length=1, max_length=1024)
    role: str = Field(default="viewer")
    cluster_ids: list[str] = Field(default_factory=list)
    daily_token_budget: int | None = None


class SetRoleRequest(_Strict):
    role: str


class SetClustersRequest(_Strict):
    cluster_ids: list[str]


class SetBudgetRequest(_Strict):
    daily_token_budget: int | None = None


def user_public(user: User) -> dict:
    """Serialize a user for API responses — never includes the password hash."""
    return {
        "id": user.id,
        "username": user.username,
        "role": user.role,
        "auth_source": user.auth_source,
        "must_change_password": user.must_change_password,
        "locked": user.locked_until is not None,
        "allowed_cluster_ids": sorted(user.allowed_cluster_ids),
        "daily_token_budget": user.daily_token_budget,
    }
