"""Card extraction fixture — functions, classes, state, and routes."""

import os
from flask import Blueprint

# Mutable module-level state (should appear in module_level_state).
counter = 0
_cache = {}
users = []

# ALL_CAPS constants — must be excluded from module_level_state.
MAX_SIZE = 100
DB_URL = "sqlite:///dev.db"

bp = Blueprint("cards", __name__)


def get_user(user_id):
    return None


def update_user(user_id, data):
    pass


class UserService:
    def process(self, user):
        pass


class OrderService:
    pass


@bp.route("/users", methods=["GET"])
def list_users():
    return []


@bp.route("/users/<int:user_id>", methods=["GET", "PUT"])
def user_detail(user_id):
    return None
