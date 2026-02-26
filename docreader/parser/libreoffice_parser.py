"""
LibreOffice Parser Module

Converts office documents (pptx, ppt, rtf, odt, etc.) to PDF via
`libreoffice --headless --convert-to pdf`, then delegates text extraction
to the existing PDFParser.  The converted PDF is optionally uploaded to
object storage so the frontend can use it for preview.
"""

import logging
import os
import shutil
import subprocess
import tempfile
import threading
import uuid
from pathlib import Path

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.pdf_parser import PDFParser

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)

# Common paths where LibreOffice may be installed
_SOFFICE_CANDIDATES = [
    # Linux
    "/usr/bin/soffice",
    "/usr/lib/libreoffice/program/soffice",
    "/opt/libreoffice25.2/program/soffice",
    # macOS
    "/Applications/LibreOffice.app/Contents/MacOS/soffice",
    # Windows
    "C:\\Program Files\\LibreOffice\\program\\soffice.exe",
    "C:\\Program Files (x86)\\LibreOffice\\program\\soffice.exe",
]


def _find_soffice() -> str:
    """Locate the LibreOffice (soffice) executable.

    Checks the LIBREOFFICE_PATH environment variable, well-known install
    locations, and finally falls back to ``which soffice``.

    Returns:
        Absolute path to the soffice binary.

    Raises:
        FileNotFoundError: If soffice cannot be found anywhere.
    """
    # 1. Explicit environment variable
    env_path = os.environ.get("LIBREOFFICE_PATH", "")
    if env_path and os.path.isfile(env_path):
        return env_path

    # 2. Well-known locations
    for candidate in _SOFFICE_CANDIDATES:
        if os.path.isfile(candidate):
            return candidate

    # 3. Resolve via PATH
    resolved = shutil.which("soffice")
    if resolved:
        return resolved

    raise FileNotFoundError(
        "LibreOffice (soffice) not found. Install LibreOffice or set "
        "the LIBREOFFICE_PATH environment variable."
    )


class LibreOfficeParser(BaseParser):
    """Parse office documents by converting them to PDF via LibreOffice.

    Supported formats include pptx, ppt, pptm, potx, potm, rtf, odt, ods,
    odp, wps, docm, dotx, dotm, xlsm, xltx, xltm, pages, numbers, key,
    vsdx, vsd, pub, hwp, hwpx, and more.

    The conversion pipeline:
        1. Write input bytes to a temp file with the correct extension.
        2. Invoke ``soffice --headless --convert-to pdf`` with a unique
           ``UserInstallation`` directory (avoids lock conflicts when
           multiple conversions run in parallel).
        3. Read the resulting PDF and delegate text extraction to
           :class:`PDFParser`.
        4. Optionally upload the converted PDF to object storage and record
           the URL in ``document.metadata["pdf_preview_path"]``.
    """

    # Limit concurrent LibreOffice processes (resource-heavy)
    _conversion_semaphore = threading.Semaphore(2)

    # Default timeout for LibreOffice conversion (seconds)
    _conversion_timeout = 120

    def parse_into_text(self, content: bytes) -> Document:
        """Convert the document to PDF and extract text.

        Args:
            content: Raw bytes of the source document.

        Returns:
            A :class:`Document` with the extracted text, chunks produced
            by the downstream :class:`PDFParser`, and optional
            ``pdf_preview_path`` metadata if storage upload succeeds.

        Raises:
            RuntimeError: If LibreOffice conversion fails or produces no PDF.
            FileNotFoundError: If the soffice binary cannot be found.
        """
        # Determine extension from file_type (strip leading dot if present)
        ext = self.file_type.lstrip(".") if self.file_type else "bin"

        soffice = _find_soffice()
        logger.info(
            "LibreOfficeParser: converting %s (%d bytes) via %s",
            self.file_name, len(content), soffice,
        )

        with tempfile.TemporaryDirectory(prefix="lo_convert_") as tmpdir:
            # --- Write source file ---
            input_path = Path(tmpdir) / f"input.{ext}"
            input_path.write_bytes(content)

            # --- Unique UserInstallation to avoid lock conflicts ---
            user_install_dir = Path(tmpdir) / f"lo_user_{uuid.uuid4().hex}"
            user_install_uri = user_install_dir.as_uri()

            # --- Build command ---
            cmd = [
                soffice,
                "--headless",
                "--norestore",
                f"-env:UserInstallation={user_install_uri}",
                "--convert-to", "pdf",
                "--outdir", tmpdir,
                str(input_path),
            ]

            # --- Run with semaphore ---
            self._conversion_semaphore.acquire()
            try:
                logger.info("LibreOfficeParser: running %s", " ".join(cmd))
                proc = subprocess.run(
                    cmd,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    timeout=self._conversion_timeout,
                )
            finally:
                self._conversion_semaphore.release()

            if proc.returncode != 0:
                stderr_text = proc.stderr.decode("utf-8", errors="replace")
                raise RuntimeError(
                    f"LibreOffice conversion failed (rc={proc.returncode}): "
                    f"{stderr_text}"
                )

            # --- Locate the output PDF ---
            pdf_files = list(Path(tmpdir).glob("*.pdf"))
            if not pdf_files:
                raise RuntimeError(
                    "No PDF output produced by LibreOffice conversion"
                )

            pdf_path = pdf_files[0]
            pdf_bytes = pdf_path.read_bytes()
            logger.info(
                "LibreOfficeParser: conversion produced %d-byte PDF",
                len(pdf_bytes),
            )

            # --- Delegate to PDFParser for text extraction ---
            pdf_parser = PDFParser(
                file_name=self.file_name.rsplit(".", 1)[0] + ".pdf",
                file_type="pdf",
                enable_multimodal=self.enable_multimodal,
                chunk_size=self.chunk_size,
                chunk_overlap=self.chunk_overlap,
                separators=self.separators,
                ocr_backend=self.ocr_backend,
                ocr_config=self.ocr_config,
                max_image_size=self.max_image_size,
                max_concurrent_tasks=self.max_concurrent_tasks,
                max_chunks=self.max_chunks,
                chunking_config=self.chunking_config,
            )
            document = pdf_parser.parse_into_text(pdf_bytes)

            # --- Upload converted PDF for frontend preview ---
            try:
                upload_url = self.storage.upload_bytes(pdf_bytes, ".pdf")
                if upload_url:
                    document.metadata["pdf_preview_path"] = upload_url
                    logger.info(
                        "LibreOfficeParser: uploaded PDF preview to %s",
                        upload_url,
                    )
                else:
                    logger.warning(
                        "LibreOfficeParser: storage.upload_bytes returned empty URL, "
                        "pdf_preview_path will not be set. storage type=%s, client=%s",
                        type(self.storage).__name__,
                        getattr(self.storage, 'client', 'N/A'),
                    )
            except Exception:
                logger.warning(
                    "LibreOfficeParser: failed to upload converted PDF",
                    exc_info=True,
                )

        return document
