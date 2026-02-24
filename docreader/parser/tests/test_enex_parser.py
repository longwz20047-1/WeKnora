import pytest
from unittest.mock import patch, MagicMock

from docreader.models.document import Document


# Minimal valid ENEX with one note
_SAMPLE_ENEX = b"""\
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE en-export SYSTEM "http://xml.evernote.com/pub/evernote-export4.dtd">
<en-export>
  <note>
    <title>My First Note</title>
    <content><![CDATA[<div>Hello from <b>Evernote</b>.</div>]]></content>
  </note>
</en-export>
"""

# ENEX with multiple notes
_SAMPLE_ENEX_MULTI = b"""\
<?xml version="1.0" encoding="UTF-8"?>
<en-export>
  <note>
    <title>Note One</title>
    <content><![CDATA[<p>First note body.</p>]]></content>
  </note>
  <note>
    <title>Note Two</title>
    <content><![CDATA[<p>Second note body.</p>]]></content>
  </note>
</en-export>
"""

# ENEX with no notes
_SAMPLE_ENEX_EMPTY = b"""\
<?xml version="1.0" encoding="UTF-8"?>
<en-export>
</en-export>
"""

# XML bomb (billion laughs attack)
_XML_BOMB = b"""\
<?xml version="1.0"?>
<!DOCTYPE lolz [
  <!ENTITY lol "lol">
  <!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">
  <!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">
  <!ENTITY lol4 "&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;">
]>
<en-export>
  <note>
    <title>Bomb</title>
    <content>&lol4;</content>
  </note>
</en-export>
"""


class TestEnexParser:
    """Tests for EnexParser - extracts text from Evernote ENEX files."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.enex_parser import EnexParser
            self.parser = EnexParser(
                file_name="test.enex",
                file_type="enex",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    def test_extracts_title_and_content(self):
        """Extracts note title and stripped HTML content."""
        # BeautifulSoup is mocked; provide a controlled side_effect
        soup_mock = MagicMock()
        soup_mock.get_text.return_value = "Hello from Evernote."

        with patch("docreader.parser.enex_parser.BeautifulSoup", return_value=soup_mock):
            result = self.parser.parse_into_text(_SAMPLE_ENEX)

        assert isinstance(result, Document)
        assert "My First Note" in result.content
        assert "Hello from Evernote." in result.content

    def test_extracts_multiple_notes(self):
        """Multiple notes are concatenated with double newlines."""
        call_count = [0]
        texts = ["First note body.", "Second note body."]

        def fake_soup(html_content, parser_name):
            mock = MagicMock()
            mock.get_text.return_value = texts[call_count[0]]
            call_count[0] += 1
            return mock

        with patch("docreader.parser.enex_parser.BeautifulSoup", side_effect=fake_soup):
            result = self.parser.parse_into_text(_SAMPLE_ENEX_MULTI)

        assert "Note One" in result.content
        assert "Note Two" in result.content
        assert "First note body." in result.content
        assert "Second note body." in result.content
        # Notes are separated
        assert "\n\n" in result.content

    def test_empty_export_returns_empty_document(self):
        """Returns empty Document when ENEX has no notes."""
        result = self.parser.parse_into_text(_SAMPLE_ENEX_EMPTY)

        assert result.content == ""
        assert isinstance(result, Document)

    def test_rejects_xml_bomb(self):
        """defusedxml rejects XML bomb (entity expansion) attacks."""
        with pytest.raises(Exception):
            self.parser.parse_into_text(_XML_BOMB)

    def test_note_without_title(self):
        """Note with no title element still extracts content."""
        enex_no_title = b"""\
<?xml version="1.0" encoding="UTF-8"?>
<en-export>
  <note>
    <content><![CDATA[<p>Content only.</p>]]></content>
  </note>
</en-export>
"""
        soup_mock = MagicMock()
        soup_mock.get_text.return_value = "Content only."

        with patch("docreader.parser.enex_parser.BeautifulSoup", return_value=soup_mock):
            result = self.parser.parse_into_text(enex_no_title)

        assert "Content only." in result.content

    def test_note_without_content(self):
        """Note with no content element still extracts title."""
        enex_no_content = b"""\
<?xml version="1.0" encoding="UTF-8"?>
<en-export>
  <note>
    <title>Title Only</title>
  </note>
</en-export>
"""
        result = self.parser.parse_into_text(enex_no_content)

        assert "Title Only" in result.content
