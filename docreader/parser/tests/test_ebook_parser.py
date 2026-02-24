import pytest
import tempfile
import shutil
import os
from unittest.mock import patch, MagicMock
from pathlib import Path

from docreader.models.document import Document


_SUBPROCESS_RUN = "docreader.parser.ebook_parser.subprocess.run"


class TestEbookParser:
    """Tests for EbookParser - converts ebook formats to text via Calibre ebook-convert."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.ebook_parser import EbookParser
            self.parser = EbookParser(
                file_name="test.azw3",
                file_type="azw3",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    @patch(_SUBPROCESS_RUN)
    def test_converts_ebook_to_text(self, mock_run):
        """ebook-convert produces a .txt file whose content becomes the Document."""

        def fake_run(cmd, **kwargs):
            # Write a fake output.txt at the expected output path
            output_path = Path(cmd[2])
            output_path.write_text("This is the ebook content.", encoding="utf-8")
            return MagicMock(returncode=0, stderr=b"")

        mock_run.side_effect = fake_run

        result = self.parser.parse_into_text(b"fake azw3 bytes")

        assert result.content == "This is the ebook content."
        assert isinstance(result, Document)
        mock_run.assert_called_once()
        call_args = mock_run.call_args[0][0]
        assert call_args[0] == "ebook-convert"
        assert call_args[1].endswith("input.azw3")
        assert call_args[2].endswith("output.txt")

    @patch(_SUBPROCESS_RUN)
    def test_raises_on_conversion_failure(self, mock_run):
        """Raises RuntimeError when ebook-convert returns non-zero exit code."""
        mock_run.return_value = MagicMock(returncode=1, stderr=b"conversion error details")

        with pytest.raises(RuntimeError, match="ebook-convert failed"):
            self.parser.parse_into_text(b"bad content")

    @patch(_SUBPROCESS_RUN)
    def test_raises_when_ebook_convert_not_found(self, mock_run):
        """Raises RuntimeError with helpful message when ebook-convert is not installed."""
        mock_run.side_effect = FileNotFoundError("No such file or directory: 'ebook-convert'")

        with pytest.raises(RuntimeError, match="ebook-convert not found.*Calibre"):
            self.parser.parse_into_text(b"fake content")

    @patch(_SUBPROCESS_RUN)
    def test_cleans_up_temp_files(self, mock_run):
        """Temp directory is cleaned up even when conversion fails."""
        mock_run.return_value = MagicMock(returncode=1, stderr=b"error")

        created_tmpdirs = []
        original_mkdtemp = tempfile.mkdtemp

        def tracking_mkdtemp(**kwargs):
            d = original_mkdtemp(**kwargs)
            created_tmpdirs.append(d)
            return d

        with patch("docreader.parser.ebook_parser.tempfile.mkdtemp", side_effect=tracking_mkdtemp):
            with pytest.raises(RuntimeError):
                self.parser.parse_into_text(b"some content")

        # The temp directory should have been cleaned up
        assert len(created_tmpdirs) == 1
        assert not os.path.exists(created_tmpdirs[0])

    @pytest.mark.parametrize("ext", ["azw3", "azw", "prc", "mobi"])
    @patch(_SUBPROCESS_RUN)
    def test_supported_formats(self, mock_run, ext):
        """Parser correctly handles each supported ebook format extension."""

        def fake_run(cmd, **kwargs):
            output_path = Path(cmd[2])
            output_path.write_text(f"content from {ext}", encoding="utf-8")
            return MagicMock(returncode=0, stderr=b"")

        mock_run.side_effect = fake_run

        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.ebook_parser import EbookParser
            parser = EbookParser(
                file_name=f"test.{ext}",
                file_type=ext,
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

        result = parser.parse_into_text(b"fake ebook bytes")

        assert result.content == f"content from {ext}"
        call_args = mock_run.call_args[0][0]
        assert call_args[1].endswith(f"input.{ext}")

    def test_semaphore_limits_concurrency(self):
        """Class-level semaphore exists with a concurrency limit of 2."""
        from docreader.parser.ebook_parser import EbookParser
        assert hasattr(EbookParser, "_conversion_semaphore")
        assert hasattr(EbookParser._conversion_semaphore, "acquire")
        assert hasattr(EbookParser._conversion_semaphore, "release")
