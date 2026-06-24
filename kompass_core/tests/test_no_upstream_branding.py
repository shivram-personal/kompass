"""Guards the Phase 0 branding requirement on the high-visibility UI shell.

This is a focused, stable gate (the entry HTML the browser loads first), not
the exhaustive repo-wide sweep — that broader sweep is tracked separately.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
INDEX_HTML = REPO_ROOT / "web" / "index.html"


def test_index_html_exists():
    assert INDEX_HTML.is_file(), f"expected {INDEX_HTML} to exist"


def test_index_html_title_is_kompass():
    html = INDEX_HTML.read_text(encoding="utf-8")
    match = re.search(r"<title>(.*?)</title>", html, re.IGNORECASE | re.DOTALL)
    assert match, "no <title> in web/index.html"
    assert "Kompass" in match.group(1)


def test_index_html_has_no_upstream_branding():
    html = INDEX_HTML.read_text(encoding="utf-8")
    assert not re.search(r"radar", html, re.IGNORECASE), (
        "upstream 'Radar' branding still present in web/index.html"
    )
