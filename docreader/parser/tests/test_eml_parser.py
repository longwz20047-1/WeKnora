import pytest
from unittest.mock import patch, MagicMock

from docreader.models.document import Document


# Minimal valid EML with plain text body
_SAMPLE_EML_PLAIN = b"""\
From: alice@example.com
To: bob@example.com
Subject: Test Email
Date: Mon, 1 Jan 2024 12:00:00 +0000
MIME-Version: 1.0
Content-Type: text/plain; charset="utf-8"

Hello Bob, this is a test email.
"""

# EML with HTML body only
_SAMPLE_EML_HTML = b"""\
From: alice@example.com
To: bob@example.com
Subject: HTML Email
Date: Mon, 1 Jan 2024 12:00:00 +0000
MIME-Version: 1.0
Content-Type: text/html; charset="utf-8"

<html><body><p>Hello from <b>HTML</b>.</p></body></html>
"""

# Multipart EML with both text and HTML
_SAMPLE_EML_MULTIPART = b"""\
From: alice@example.com
To: bob@example.com
Subject: Multipart Email
Date: Mon, 1 Jan 2024 12:00:00 +0000
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="boundary123"

--boundary123
Content-Type: text/plain; charset="utf-8"

Plain text body.
--boundary123
Content-Type: text/html; charset="utf-8"

<html><body><p>HTML body.</p></body></html>
--boundary123--
"""

# EML with no body
_SAMPLE_EML_HEADERS_ONLY = b"""\
From: alice@example.com
Subject: No Body
Date: Mon, 1 Jan 2024 12:00:00 +0000
MIME-Version: 1.0
Content-Type: text/plain; charset="utf-8"

"""


class TestEmlParser:
    """Tests for EmlParser - extracts text from EML email files."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.eml_parser import EmlParser
            self.parser = EmlParser(
                file_name="test.eml",
                file_type="eml",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    def test_extracts_plain_text_body(self):
        """Extracts plain text body and headers from a simple EML."""
        result = self.parser.parse_into_text(_SAMPLE_EML_PLAIN)

        assert isinstance(result, Document)
        assert "Hello Bob, this is a test email." in result.content
        assert "Subject: Test Email" in result.content
        assert "From: alice@example.com" in result.content
        assert result.metadata["subject"] == "Test Email"
        assert result.metadata["from"] == "alice@example.com"

    def test_extracts_html_body_as_text(self):
        """HTML-only EML has its HTML stripped to plain text."""
        # BeautifulSoup is mocked at import level; we need to provide a
        # real-ish implementation for the HTML stripping.
        soup_mock = MagicMock()
        soup_mock.get_text.return_value = "Hello from HTML."

        with patch("docreader.parser.eml_parser.BeautifulSoup", return_value=soup_mock):
            result = self.parser.parse_into_text(_SAMPLE_EML_HTML)

        assert "Hello from HTML." in result.content
        assert "Subject: HTML Email" in result.content

    def test_prefers_plain_text_in_multipart(self):
        """For multipart/alternative, plain text part is preferred over HTML."""
        result = self.parser.parse_into_text(_SAMPLE_EML_MULTIPART)

        assert "Plain text body." in result.content
        assert "Subject: Multipart Email" in result.content

    def test_headers_only_eml(self):
        """EML with empty body still extracts headers."""
        result = self.parser.parse_into_text(_SAMPLE_EML_HEADERS_ONLY)

        assert "Subject: No Body" in result.content
        assert isinstance(result, Document)

    def test_metadata_fields(self):
        """Metadata dict contains subject, from, and date."""
        result = self.parser.parse_into_text(_SAMPLE_EML_PLAIN)

        assert "subject" in result.metadata
        assert "from" in result.metadata
        assert "date" in result.metadata
