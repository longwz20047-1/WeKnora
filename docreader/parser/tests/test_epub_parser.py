import pytest
from unittest.mock import patch, MagicMock, call

from docreader.models.document import Document


def _make_soup_factory(text_map):
    """Create a BeautifulSoup mock factory that returns controlled text.

    Args:
        text_map: dict mapping HTML bytes to text output
    """
    def fake_soup(html_content, parser_name):
        mock_soup = MagicMock()
        text = text_map.get(html_content, "")
        mock_soup.get_text.return_value = text
        return mock_soup
    return fake_soup


class TestEpubParser:
    """Tests for EpubParser - extracts text from EPUB ebook files."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.epub_parser import EpubParser
            self.parser = EpubParser(
                file_name="test.epub",
                file_type="epub",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    @patch("docreader.parser.epub_parser.tempfile")
    @patch("docreader.parser.epub_parser.os")
    def test_extracts_text_from_chapters(self, mock_os, mock_tempfile):
        """Extracts and concatenates text from all ITEM_DOCUMENT chapters."""
        import ebooklib

        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.epub")
        mock_os.path.exists.return_value = True

        html1 = b"<html><body><p>Chapter One</p></body></html>"
        html2 = b"<html><body><p>Chapter Two</p></body></html>"

        item1 = MagicMock()
        item1.get_content.return_value = html1
        item2 = MagicMock()
        item2.get_content.return_value = html2

        mock_book = MagicMock()
        mock_book.get_items_of_type.return_value = [item1, item2]

        soup_factory = _make_soup_factory({
            html1: "Chapter One",
            html2: "Chapter Two",
        })

        with patch("docreader.parser.epub_parser.epub") as mock_epub_mod, \
             patch("docreader.parser.epub_parser.BeautifulSoup", side_effect=soup_factory):
            mock_epub_mod.read_epub.return_value = mock_book

            result = self.parser.parse_into_text(b"fake epub bytes")

        assert "Chapter One" in result.content
        assert "Chapter Two" in result.content
        assert "\n\n" in result.content
        mock_book.get_items_of_type.assert_called_once_with(ebooklib.ITEM_DOCUMENT)

    @patch("docreader.parser.epub_parser.tempfile")
    @patch("docreader.parser.epub_parser.os")
    def test_skips_empty_chapters(self, mock_os, mock_tempfile):
        """Items with only whitespace are skipped."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.epub")
        mock_os.path.exists.return_value = True

        html_real = b"<html><body>Real content</body></html>"
        html_empty = b"<html><body>   </body></html>"

        item_nonempty = MagicMock()
        item_nonempty.get_content.return_value = html_real
        item_empty = MagicMock()
        item_empty.get_content.return_value = html_empty

        mock_book = MagicMock()
        mock_book.get_items_of_type.return_value = [item_nonempty, item_empty]

        soup_factory = _make_soup_factory({
            html_real: "Real content",
            html_empty: "   ",
        })

        with patch("docreader.parser.epub_parser.epub") as mock_epub_mod, \
             patch("docreader.parser.epub_parser.BeautifulSoup", side_effect=soup_factory):
            mock_epub_mod.read_epub.return_value = mock_book

            result = self.parser.parse_into_text(b"fake epub")

        assert result.content.strip() == "Real content"

    @patch("docreader.parser.epub_parser.tempfile")
    @patch("docreader.parser.epub_parser.os")
    def test_returns_empty_document_when_no_chapters(self, mock_os, mock_tempfile):
        """Returns empty Document when EPUB has no document items."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.epub")
        mock_os.path.exists.return_value = True

        mock_book = MagicMock()
        mock_book.get_items_of_type.return_value = []

        with patch("docreader.parser.epub_parser.epub") as mock_epub_mod:
            mock_epub_mod.read_epub.return_value = mock_book

            result = self.parser.parse_into_text(b"fake epub")

        assert result.content == ""

    @patch("docreader.parser.epub_parser.tempfile")
    @patch("docreader.parser.epub_parser.os")
    def test_cleans_up_temp_file(self, mock_os, mock_tempfile):
        """Temporary file is removed after parsing."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.epub")
        mock_os.path.exists.return_value = True

        mock_book = MagicMock()
        mock_book.get_items_of_type.return_value = []

        with patch("docreader.parser.epub_parser.epub") as mock_epub_mod:
            mock_epub_mod.read_epub.return_value = mock_book

            self.parser.parse_into_text(b"fake epub")

        mock_os.remove.assert_called_once_with("/tmp/fake.epub")

    @patch("docreader.parser.epub_parser.tempfile")
    @patch("docreader.parser.epub_parser.os")
    def test_cleans_up_temp_file_on_error(self, mock_os, mock_tempfile):
        """Temporary file is removed even when parsing fails."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.epub")
        mock_os.path.exists.return_value = True

        with patch("docreader.parser.epub_parser.epub") as mock_epub_mod:
            mock_epub_mod.read_epub.side_effect = Exception("corrupt epub")

            with pytest.raises(Exception, match="corrupt epub"):
                self.parser.parse_into_text(b"bad data")

        mock_os.remove.assert_called_once_with("/tmp/fake.epub")

    def test_returns_document_type(self):
        """parse_into_text returns a Document instance."""
        with patch("docreader.parser.epub_parser.tempfile") as mock_tempfile, \
             patch("docreader.parser.epub_parser.os") as mock_os, \
             patch("docreader.parser.epub_parser.epub") as mock_epub_mod:
            mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.epub")
            mock_os.path.exists.return_value = True
            mock_book = MagicMock()
            mock_book.get_items_of_type.return_value = []
            mock_epub_mod.read_epub.return_value = mock_book

            result = self.parser.parse_into_text(b"epub")

        assert isinstance(result, Document)
