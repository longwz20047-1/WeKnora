import logging
import os
import tempfile

import ebooklib
from bs4 import BeautifulSoup
from ebooklib import epub

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class EpubParser(BaseParser):
    """
    EPUB ebook parser.

    Uses ebooklib to read EPUB files and BeautifulSoup to extract text
    from each HTML document item. Since ebooklib requires a file path,
    the raw bytes are written to a temporary file first.
    """

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse EPUB content by extracting text from all document items.

        Args:
            content: Raw EPUB file content as bytes

        Returns:
            Document containing the concatenated text of all chapters
        """
        logger.info(f"Parsing EPUB document, content size: {len(content)} bytes")

        # ebooklib requires a file path, so write bytes to a temp file
        tmp_fd, tmp_path = tempfile.mkstemp(suffix=".epub")
        try:
            os.write(tmp_fd, content)
            os.close(tmp_fd)

            book = epub.read_epub(tmp_path)

            chapter_texts = []
            for item in book.get_items_of_type(ebooklib.ITEM_DOCUMENT):
                html_content = item.get_content()
                soup = BeautifulSoup(html_content, "html.parser")
                text = soup.get_text()
                stripped = text.strip()
                if stripped:
                    chapter_texts.append(stripped)

            full_text = "\n\n".join(chapter_texts)
            logger.info(
                f"Successfully parsed EPUB, extracted {len(full_text)} characters "
                f"from {len(chapter_texts)} chapters"
            )
            return Document(content=full_text)
        finally:
            # Clean up temp file
            if os.path.exists(tmp_path):
                os.remove(tmp_path)
