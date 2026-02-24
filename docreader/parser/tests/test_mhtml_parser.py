import pytest
from unittest.mock import patch, MagicMock

from docreader.models.document import Document


# Minimal MHTML with HTML content
_SAMPLE_MHTML = b"""\
From: <Saved by Browser>
Subject: Test Page
Date: Mon, 1 Jan 2024 00:00:00 +0000
MIME-Version: 1.0
Content-Type: multipart/related; boundary="----=_Part_001"

------=_Part_001
Content-Type: text/html; charset="utf-8"
Content-Transfer-Encoding: 7bit

<html><body><h1>Hello World</h1><p>This is a saved page.</p></body></html>
------=_Part_001--
"""

# MHTML with plain text part only
_SAMPLE_MHTML_PLAIN = b"""\
From: <Saved by Browser>
Subject: Plain Page
MIME-Version: 1.0
Content-Type: text/plain; charset="utf-8"

Just plain text content.
"""

# MHTML with no text content (only image parts)
_SAMPLE_MHTML_NO_TEXT = b"""\
From: <Saved by Browser>
Subject: Image Only
MIME-Version: 1.0
Content-Type: multipart/related; boundary="----=_Part_001"

------=_Part_001
Content-Type: image/png
Content-Transfer-Encoding: base64

iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==
------=_Part_001--
"""


class TestMhtmlParser:
    """Tests for MhtmlParser - extracts text from MHTML web archives."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.mhtml_parser import MhtmlParser
            self.parser = MhtmlParser(
                file_name="test.mhtml",
                file_type="mhtml",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    def test_extracts_text_from_html_part(self):
        """Extracts and strips HTML content from MHTML."""
        soup_mock = MagicMock()
        soup_mock.get_text.return_value = "Hello World\nThis is a saved page."

        with patch("docreader.parser.mhtml_parser.BeautifulSoup", return_value=soup_mock):
            result = self.parser.parse_into_text(_SAMPLE_MHTML)

        assert isinstance(result, Document)
        assert "Hello World" in result.content
        assert "This is a saved page." in result.content

    def test_extracts_plain_text_fallback(self):
        """Falls back to text/plain when no HTML parts exist."""
        result = self.parser.parse_into_text(_SAMPLE_MHTML_PLAIN)

        assert "Just plain text content." in result.content

    def test_returns_empty_for_no_text_content(self):
        """Returns empty Document when no text/html or text/plain parts."""
        result = self.parser.parse_into_text(_SAMPLE_MHTML_NO_TEXT)

        assert result.content == ""
        assert isinstance(result, Document)

    def test_returns_document_type(self):
        """parse_into_text returns a Document instance."""
        soup_mock = MagicMock()
        soup_mock.get_text.return_value = "text"

        with patch("docreader.parser.mhtml_parser.BeautifulSoup", return_value=soup_mock):
            result = self.parser.parse_into_text(_SAMPLE_MHTML)

        assert isinstance(result, Document)
