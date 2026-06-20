"""Flask application entry point."""

from utils import helper        # bare import
from models import SomeModel    # bare import — SomeModel is a class, falls back to models
import flask                    # external
