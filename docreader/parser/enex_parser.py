import logging

import defusedxml.ElementTree as ET
from bs4 import BeautifulSoup

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class EnexParser(BaseParser):
    """
    ENEX (Evernote export) parser.

    Uses defusedxml to safely parse ENEX XML files (preventing XML bomb
    and XXE attacks).  Extracts title and content from each ``<note>``
    element, stripping embedded XHTML via BeautifulSoup.
    """

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse ENEX content by extracting notes from the XML structure.

        ENEX files contain ``<note>`` elements, each with a ``<title>``
        and a ``<content>`` child.  The ``<content>`` element holds CDATA
        with XHTML markup that is stripped to plain text.

        Args:
            content: Raw ENEX file content as bytes

        Returns:
            Document containing the concatenated text of all notes
        """
        logger.info(f"Parsing ENEX document, content size: {len(content)} bytes")

        root = ET.fromstring(content)

        note_texts: list[str] = []
        for note in root.iter("note"):
            title_el = note.find("title")
            content_el = note.find("content")

            parts: list[str] = []
            if title_el is not None and title_el.text:
                parts.append(title_el.text.strip())

            if content_el is not None and content_el.text:
                soup = BeautifulSoup(content_el.text, "html.parser")
                body_text = soup.get_text(separator="\n").strip()
                if body_text:
                    parts.append(body_text)

            if parts:
                note_texts.append("\n".join(parts))

        full_text = "\n\n".join(note_texts)
        logger.info(
            f"Successfully parsed ENEX, extracted {len(full_text)} characters "
            f"from {len(note_texts)} notes"
        )
        return Document(content=full_text)
