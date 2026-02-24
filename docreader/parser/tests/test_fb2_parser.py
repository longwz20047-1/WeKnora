import pytest
from unittest.mock import patch, MagicMock

from docreader.models.document import Document


# Minimal valid FB2 XML with namespace
_SAMPLE_FB2 = b"""\
<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <body>
    <section>
      <p>First paragraph.</p>
      <p>Second paragraph with <emphasis>emphasis</emphasis>.</p>
    </section>
    <section>
      <p>Third paragraph.</p>
    </section>
  </body>
</FictionBook>
"""

# FB2 without namespace (some real-world files omit it)
_SAMPLE_FB2_NO_NS = b"""\
<?xml version="1.0" encoding="UTF-8"?>
<FictionBook>
  <body>
    <section>
      <p>No namespace paragraph.</p>
    </section>
  </body>
</FictionBook>
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
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <body><section><p>&lol4;</p></section></body>
</FictionBook>
"""


class TestFb2Parser:
    """Tests for Fb2Parser - extracts text from FB2 ebook files."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.fb2_parser import Fb2Parser
            self.parser = Fb2Parser(
                file_name="test.fb2",
                file_type="fb2",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    def test_extracts_paragraphs_from_namespaced_fb2(self):
        """Extracts all <p> elements from FB2 body with namespace."""
        result = self.parser.parse_into_text(_SAMPLE_FB2)

        assert "First paragraph." in result.content
        assert "Second paragraph with emphasis." in result.content
        assert "Third paragraph." in result.content
        assert isinstance(result, Document)

    def test_extracts_inline_elements(self):
        """Inline elements like <emphasis> are included as text."""
        result = self.parser.parse_into_text(_SAMPLE_FB2)

        # The emphasis text should be extracted inline
        assert "emphasis" in result.content

    def test_fallback_without_namespace(self):
        """Falls back to non-namespaced search when namespace yields nothing."""
        result = self.parser.parse_into_text(_SAMPLE_FB2_NO_NS)

        assert "No namespace paragraph." in result.content

    def test_empty_body_returns_empty_document(self):
        """Returns empty Document when body contains no paragraphs."""
        empty_fb2 = b"""\
<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <body></body>
</FictionBook>
"""
        result = self.parser.parse_into_text(empty_fb2)

        assert result.content == ""

    def test_rejects_xml_bomb(self):
        """defusedxml rejects XML bomb (entity expansion) attacks."""
        with pytest.raises(Exception):
            # defusedxml should raise EntitiesForbidden or similar
            self.parser.parse_into_text(_XML_BOMB)

    def test_multiple_body_sections(self):
        """Extracts text from all body sections (main + footnotes)."""
        multi_body = b"""\
<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <body>
    <section><p>Main text.</p></section>
  </body>
  <body name="notes">
    <section><p>Footnote text.</p></section>
  </body>
</FictionBook>
"""
        result = self.parser.parse_into_text(multi_body)

        assert "Main text." in result.content
        assert "Footnote text." in result.content

    def test_paragraphs_joined_by_newline(self):
        """Paragraphs are joined by single newline."""
        result = self.parser.parse_into_text(_SAMPLE_FB2)

        # Each paragraph on its own line
        lines = result.content.split("\n")
        assert len(lines) >= 3
