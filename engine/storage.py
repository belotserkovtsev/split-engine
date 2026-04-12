"""Storage skeleton for split-engine."""

from pathlib import Path
import sqlite3

DB_PATH = Path(__file__).resolve().parents[1] / 'state' / 'split-engine.db'


def connect() -> sqlite3.Connection:
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    return conn
