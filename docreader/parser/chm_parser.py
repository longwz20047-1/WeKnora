import logging
import os
import tempfile

import chm
from bs4 import BeautifulSoup

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class ChmParser(BaseParser):
    """
    CHM (Compiled HTML Help) file parser.

    Uses pychm to open CHM files and BeautifulSoup to extract text
    from embedded HTML pages. Since pychm requires a file path,
    the raw bytes are written to a temporary file first.
    """

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse CHM content by extracting text from all HTML objects.

        Args:
            content: Raw CHM file content as bytes

        Returns:
            Document containing the concatenated text of all HTML pages
        """
        logger.info(f"Parsing CHM document, content size: {len(content)} bytes")

        tmp_fd, tmp_path = tempfile.mkstemp(suffix=".chm")
        try:
            os.write(tmp_fd, content)
            os.close(tmp_fd)

            chm_file = chm.CHMFile()
            chm_file.LoadCHM(tmp_path)

            html_texts = []

            def _enumerator(chm_file_obj, ui, context):
                """Callback for chm.CHMFile.EnumerateDir to collect HTML paths."""
                path = ui.path.decode("utf-8") if isinstance(ui.path, bytes) else ui.path
                if path.lower().endswith((".html", ".htm")):
                    context.append(path)
                return chm.CHM_ENUMERATOR_CONTINUE

            paths = []
            chm_file.EnumerateDir("/", _enumerator, paths)

            for path in paths:
                try:
                    result, ui = chm_file.ResolveObject(path)
                    if result != chm.CHM_RESOLVE_SUCCESS:
                        continue
                    result, data = chm_file.RetrieveObject(ui)
                    if result == 0 or not data:
                        continue
                    html_bytes = data if isinstance(data, bytes) else data.encode("utf-8")
                    soup = BeautifulSoup(html_bytes, "html.parser")
                    text = soup.get_text().strip()
                    if text:
                        html_texts.append(text)
                except Exception as e:
                    logger.warning(f"Failed to extract CHM object '{path}': {e}")

            chm_file.CloseCHM()

            full_text = "\n\n".join(html_texts)
            logger.info(
                f"Successfully parsed CHM, extracted {len(full_text)} characters "
                f"from {len(html_texts)} HTML pages"
            )
            return Document(content=full_text)
        finally:
            if os.path.exists(tmp_path):
                os.remove(tmp_path)
