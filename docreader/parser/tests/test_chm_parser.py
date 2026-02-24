import pytest
from unittest.mock import patch, MagicMock

from docreader.models.document import Document


def _make_soup_factory(html_to_text):
    """Create a BeautifulSoup mock factory that maps HTML bytes to text."""
    def fake_soup(html_content, parser_name):
        mock_soup = MagicMock()
        # html_content can be bytes or str
        key = html_content if isinstance(html_content, bytes) else html_content.encode("utf-8")
        text = html_to_text.get(key, "")
        mock_soup.get_text.return_value = text
        return mock_soup
    return fake_soup


class TestChmParser:
    """Tests for ChmParser - extracts text from CHM (Compiled HTML Help) files."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.chm_parser import ChmParser
            self.parser = ChmParser(
                file_name="test.chm",
                file_type="chm",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    @patch("docreader.parser.chm_parser.tempfile")
    @patch("docreader.parser.chm_parser.os")
    def test_extracts_text_from_html_pages(self, mock_os, mock_tempfile):
        """Extracts and concatenates text from all HTML objects in CHM."""
        import chm as chm_mod

        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.chm")
        mock_os.path.exists.return_value = True

        mock_chm = MagicMock()

        html1 = b"<html><body><p>Page One</p></body></html>"
        html2 = b"<html><body><p>Page Two</p></body></html>"

        def fake_enumerate(root, callback, context):
            ui1 = MagicMock()
            ui1.path = "/page1.html"
            callback(mock_chm, ui1, context)
            ui2 = MagicMock()
            ui2.path = "/page2.htm"
            callback(mock_chm, ui2, context)
            ui3 = MagicMock()
            ui3.path = "/image.png"  # non-HTML, should be skipped
            callback(mock_chm, ui3, context)

        mock_chm.EnumerateDir.side_effect = fake_enumerate

        mock_ui = MagicMock()
        mock_chm.ResolveObject.return_value = (chm_mod.CHM_RESOLVE_SUCCESS, mock_ui)

        call_count = [0]

        def fake_retrieve(ui):
            call_count[0] += 1
            if call_count[0] == 1:
                return (1, html1)
            else:
                return (1, html2)

        mock_chm.RetrieveObject.side_effect = fake_retrieve

        soup_factory = _make_soup_factory({
            html1: "Page One",
            html2: "Page Two",
        })

        with patch("docreader.parser.chm_parser.chm") as mock_chm_mod, \
             patch("docreader.parser.chm_parser.BeautifulSoup", side_effect=soup_factory):
            mock_chm_mod.CHMFile.return_value = mock_chm
            mock_chm_mod.CHM_ENUMERATOR_CONTINUE = chm_mod.CHM_ENUMERATOR_CONTINUE
            mock_chm_mod.CHM_RESOLVE_SUCCESS = chm_mod.CHM_RESOLVE_SUCCESS

            result = self.parser.parse_into_text(b"fake chm bytes")

        assert "Page One" in result.content
        assert "Page Two" in result.content
        assert "\n\n" in result.content
        mock_chm.CloseCHM.assert_called_once()

    @patch("docreader.parser.chm_parser.tempfile")
    @patch("docreader.parser.chm_parser.os")
    def test_returns_empty_when_no_html(self, mock_os, mock_tempfile):
        """Returns empty Document when CHM contains no HTML files."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.chm")
        mock_os.path.exists.return_value = True

        mock_chm = MagicMock()
        mock_chm.EnumerateDir.side_effect = lambda root, cb, ctx: None

        with patch("docreader.parser.chm_parser.chm") as mock_chm_mod:
            mock_chm_mod.CHMFile.return_value = mock_chm

            result = self.parser.parse_into_text(b"empty chm")

        assert result.content == ""
        assert isinstance(result, Document)

    @patch("docreader.parser.chm_parser.tempfile")
    @patch("docreader.parser.chm_parser.os")
    def test_cleans_up_temp_file(self, mock_os, mock_tempfile):
        """Temporary file is removed after parsing."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.chm")
        mock_os.path.exists.return_value = True

        mock_chm = MagicMock()
        mock_chm.EnumerateDir.side_effect = lambda root, cb, ctx: None

        with patch("docreader.parser.chm_parser.chm") as mock_chm_mod:
            mock_chm_mod.CHMFile.return_value = mock_chm

            self.parser.parse_into_text(b"chm data")

        mock_os.remove.assert_called_once_with("/tmp/fake.chm")

    @patch("docreader.parser.chm_parser.tempfile")
    @patch("docreader.parser.chm_parser.os")
    def test_handles_resolve_failure_gracefully(self, mock_os, mock_tempfile):
        """Skips objects that fail to resolve without crashing."""
        import chm as chm_mod

        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.chm")
        mock_os.path.exists.return_value = True

        mock_chm = MagicMock()

        def fake_enumerate(root, callback, context):
            ui = MagicMock()
            ui.path = "/bad.html"
            callback(mock_chm, ui, context)

        mock_chm.EnumerateDir.side_effect = fake_enumerate
        mock_chm.ResolveObject.return_value = (999, None)

        with patch("docreader.parser.chm_parser.chm") as mock_chm_mod:
            mock_chm_mod.CHMFile.return_value = mock_chm
            mock_chm_mod.CHM_ENUMERATOR_CONTINUE = chm_mod.CHM_ENUMERATOR_CONTINUE
            mock_chm_mod.CHM_RESOLVE_SUCCESS = chm_mod.CHM_RESOLVE_SUCCESS

            result = self.parser.parse_into_text(b"chm with bad object")

        assert result.content == ""
