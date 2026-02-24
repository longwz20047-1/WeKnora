"""
Ebook Parser Module

Converts ebook formats (azw3, azw, prc, mobi) to plain text via
Calibre's ``ebook-convert`` CLI tool.
"""

import logging
import os
import shutil
import subprocess
import tempfile
import threading
from pathlib import Path

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class EbookParser(BaseParser):
    """Parse ebook files by converting them to plain text via Calibre.

    Supported formats include azw3, azw, prc, and mobi.

    The conversion pipeline:
        1. Write input bytes to a temp file with the correct extension.
        2. Invoke ``ebook-convert input.{ext} output.txt``.
        3. Read the resulting .txt file and return its content as a Document.
        4. Clean up all temporary files.
    """

    # Limit concurrent ebook-convert processes (resource-heavy)
    _conversion_semaphore = threading.Semaphore(2)

    # Default timeout for ebook-convert (seconds)
    _conversion_timeout = 120

    def parse_into_text(self, content: bytes) -> Document:
        """Convert the ebook to plain text and return the content.

        Args:
            content: Raw bytes of the source ebook file.

        Returns:
            A :class:`Document` with the extracted text content.

        Raises:
            RuntimeError: If ebook-convert is not found or conversion fails.
        """
        # Determine extension from file_type (strip leading dot if present)
        ext = self.file_type.lstrip(".") if self.file_type else "bin"

        logger.info(
            "EbookParser: converting %s (%d bytes) via ebook-convert",
            self.file_name, len(content),
        )

        tmpdir = tempfile.mkdtemp(prefix="ebook_convert_")
        try:
            # --- Write source file ---
            input_path = Path(tmpdir) / f"input.{ext}"
            input_path.write_bytes(content)

            # --- Output path ---
            output_path = Path(tmpdir) / "output.txt"

            # --- Build command ---
            cmd = [
                "ebook-convert",
                str(input_path),
                str(output_path),
            ]

            # --- Run with semaphore ---
            self._conversion_semaphore.acquire()
            try:
                logger.info("EbookParser: running %s", " ".join(cmd))
                proc = subprocess.run(
                    cmd,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    timeout=self._conversion_timeout,
                )
            except FileNotFoundError:
                raise RuntimeError(
                    "ebook-convert not found. Please install Calibre "
                    "(https://calibre-ebook.com/) and ensure ebook-convert "
                    "is on your PATH."
                )
            finally:
                self._conversion_semaphore.release()

            if proc.returncode != 0:
                stderr_text = proc.stderr.decode("utf-8", errors="replace")
                raise RuntimeError(
                    f"ebook-convert failed (rc={proc.returncode}): "
                    f"{stderr_text}"
                )

            # --- Read output text ---
            if not output_path.exists():
                raise RuntimeError(
                    "No text output produced by ebook-convert"
                )

            text_content = output_path.read_text(encoding="utf-8", errors="replace")
            logger.info(
                "EbookParser: extracted %d characters from %s",
                len(text_content), self.file_name,
            )

            return Document(content=text_content)

        finally:
            # --- Clean up temp directory ---
            shutil.rmtree(tmpdir, ignore_errors=True)
