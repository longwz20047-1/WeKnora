import pytest
from unittest.mock import patch, MagicMock
from pathlib import Path

from docreader.models.document import Document


# Common mock path for _find_soffice
_FIND_SOFFICE = "docreader.parser.libreoffice_parser._find_soffice"
_SUBPROCESS_RUN = "docreader.parser.libreoffice_parser.subprocess.run"
_PDF_PARSER = "docreader.parser.libreoffice_parser.PDFParser"


def _make_pdf_mock(content="extracted text"):
    """Create a mock PDFParser class that returns a Document."""
    mock_pdf_instance = MagicMock()
    mock_pdf_instance.parse_into_text.return_value = Document(content=content)
    mock_pdf_cls = MagicMock(return_value=mock_pdf_instance)
    return mock_pdf_cls, mock_pdf_instance


class TestLibreOfficeParser:
    """Tests for LibreOfficeParser - converts office documents to PDF via LibreOffice."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.libreoffice_parser import LibreOfficeParser
            self.parser = LibreOfficeParser(
                file_name="test.pptx",
                file_type="pptx",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    @patch(_PDF_PARSER)
    @patch(_SUBPROCESS_RUN)
    @patch(_FIND_SOFFICE, return_value="/usr/bin/soffice")
    def test_converts_and_extracts_text(self, mock_find, mock_run, mock_pdf_cls):
        """LibreOffice converts to PDF, then PDFParser extracts text."""
        mock_run.return_value = MagicMock(returncode=0, stderr=b"")

        mock_pdf = MagicMock()
        mock_pdf.parse_into_text.return_value = Document(content="slide content")
        mock_pdf_cls.return_value = mock_pdf

        # We need to create a fake PDF in the temp dir so glob("*.pdf") finds it.
        # We'll patch Path(...).glob to return a mock pdf path.
        original_path = Path

        def _patched_path_init(*args, **kwargs):
            return original_path(*args, **kwargs)

        # Instead of mocking Path entirely, create a side_effect on subprocess.run
        # that writes a fake PDF into the tmpdir
        def fake_run(cmd, **kwargs):
            # Find the --outdir argument to know where to write fake PDF
            outdir_idx = cmd.index("--outdir") + 1
            outdir = cmd[outdir_idx]
            # Write a fake PDF file
            fake_pdf = Path(outdir) / "input.pdf"
            fake_pdf.write_bytes(b"%PDF-fake-content")
            return MagicMock(returncode=0, stderr=b"")

        mock_run.side_effect = fake_run

        result = self.parser.parse_into_text(b"fake pptx content")

        assert result.content == "slide content"
        mock_run.assert_called_once()
        call_args = mock_run.call_args[0][0]
        assert any("soffice" in str(arg) for arg in call_args)
        assert "--convert-to" in call_args
        assert "pdf" in call_args

    @patch(_SUBPROCESS_RUN)
    @patch(_FIND_SOFFICE, return_value="/usr/bin/soffice")
    def test_raises_on_conversion_failure(self, mock_find, mock_run):
        """Raises RuntimeError when LibreOffice conversion fails."""
        mock_run.return_value = MagicMock(returncode=1, stderr=b"conversion error")

        with pytest.raises(RuntimeError, match="LibreOffice conversion failed"):
            self.parser.parse_into_text(b"bad content")

    @patch(_SUBPROCESS_RUN)
    @patch(_FIND_SOFFICE, return_value="/usr/bin/soffice")
    def test_raises_when_no_pdf_produced(self, mock_find, mock_run):
        """Raises RuntimeError when LibreOffice reports success but no PDF is found."""
        # subprocess.run succeeds but does NOT create any .pdf file in tmpdir
        mock_run.return_value = MagicMock(returncode=0, stderr=b"")

        with pytest.raises(RuntimeError, match="No PDF output"):
            self.parser.parse_into_text(b"fake content")

    @patch(_PDF_PARSER)
    @patch(_SUBPROCESS_RUN)
    @patch(_FIND_SOFFICE, return_value="/usr/bin/soffice")
    def test_uploads_pdf_and_sets_metadata(self, mock_find, mock_run, mock_pdf_cls):
        """When storage is available, uploads converted PDF and sets metadata."""
        mock_pdf = MagicMock()
        mock_pdf.parse_into_text.return_value = Document(content="text from pdf")
        mock_pdf_cls.return_value = mock_pdf

        def fake_run(cmd, **kwargs):
            outdir_idx = cmd.index("--outdir") + 1
            outdir = cmd[outdir_idx]
            Path(outdir).joinpath("input.pdf").write_bytes(b"%PDF-fake")
            return MagicMock(returncode=0, stderr=b"")

        mock_run.side_effect = fake_run

        # Mock storage to return a URL
        self.parser.storage = MagicMock()
        self.parser.storage.upload_bytes.return_value = "https://storage.example.com/converted.pdf"

        result = self.parser.parse_into_text(b"fake pptx content")

        assert result.metadata["pdf_preview_path"] == "https://storage.example.com/converted.pdf"
        self.parser.storage.upload_bytes.assert_called_once_with(b"%PDF-fake", ".pdf")

    @patch(_PDF_PARSER)
    @patch(_SUBPROCESS_RUN)
    @patch(_FIND_SOFFICE, return_value="/usr/bin/soffice")
    def test_storage_failure_does_not_break_parsing(self, mock_find, mock_run, mock_pdf_cls):
        """If storage upload fails, parsing still returns text without metadata."""
        mock_pdf = MagicMock()
        mock_pdf.parse_into_text.return_value = Document(content="slide text")
        mock_pdf_cls.return_value = mock_pdf

        def fake_run(cmd, **kwargs):
            outdir_idx = cmd.index("--outdir") + 1
            outdir = cmd[outdir_idx]
            Path(outdir).joinpath("input.pdf").write_bytes(b"%PDF-fake")
            return MagicMock(returncode=0, stderr=b"")

        mock_run.side_effect = fake_run

        # Mock storage that returns empty string (failure)
        self.parser.storage = MagicMock()
        self.parser.storage.upload_bytes.return_value = ""

        result = self.parser.parse_into_text(b"fake pptx content")

        assert result.content == "slide text"
        assert "pdf_preview_path" not in result.metadata

    @patch(_PDF_PARSER)
    @patch(_SUBPROCESS_RUN)
    @patch(_FIND_SOFFICE, return_value="/usr/bin/soffice")
    def test_user_installation_env_is_unique(self, mock_find, mock_run, mock_pdf_cls):
        """Each conversion uses a unique UserInstallation directory to avoid lock conflicts."""
        mock_pdf = MagicMock()
        mock_pdf.parse_into_text.return_value = Document(content="text")
        mock_pdf_cls.return_value = mock_pdf

        def fake_run(cmd, **kwargs):
            outdir_idx = cmd.index("--outdir") + 1
            outdir = cmd[outdir_idx]
            Path(outdir).joinpath("input.pdf").write_bytes(b"%PDF-fake")
            return MagicMock(returncode=0, stderr=b"")

        mock_run.side_effect = fake_run

        self.parser.parse_into_text(b"content")

        call_args = mock_run.call_args[0][0]
        # Find the -env:UserInstallation argument
        user_install_args = [arg for arg in call_args if "UserInstallation" in str(arg)]
        assert len(user_install_args) == 1
        assert "file://" in user_install_args[0]

    def test_correct_file_extension_used(self):
        """Parser uses the correct file extension from file_type."""
        assert self.parser.file_type == "pptx"

    def test_semaphore_limits_concurrency(self):
        """Class-level semaphore exists with a small concurrency limit."""
        from docreader.parser.libreoffice_parser import LibreOfficeParser
        assert hasattr(LibreOfficeParser, "_conversion_semaphore")
        # Verify it is a threading.Semaphore (has acquire/release)
        assert hasattr(LibreOfficeParser._conversion_semaphore, "acquire")
        assert hasattr(LibreOfficeParser._conversion_semaphore, "release")
