"""Initial schema: users, authors, books, book<->author, isbn registry, audit log.

Revision ID: 0001
Revises:
Create Date: 2026-11-17
"""

from pathlib import Path

from alembic import op

revision = "0001"
down_revision = None
branch_labels = None
depends_on = None

_SQL_DIR = Path(__file__).resolve().parent.parent / "sql"


def upgrade() -> None:
    op.execute((_SQL_DIR / "0001_initial.up.sql").read_text())


def downgrade() -> None:
    op.execute((_SQL_DIR / "0001_initial.down.sql").read_text())
