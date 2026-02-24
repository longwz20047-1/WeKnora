import logging

import defusedxml.ElementTree as ET

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)

# FictionBook 2.0 XML namespace
_FB2_NS = "http://www.gribuser.ru/xml/fictionbook/2.0"


class Fb2Parser(BaseParser):
    """
    FB2 (FictionBook 2.0) ebook parser.

    Uses defusedxml to safely parse FB2 XML files (preventing XML bomb
    and XXE attacks). Extracts text from all <p> elements within <body>
    sections.
    """

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse FB2 content by extracting paragraph text from body sections.

        Args:
            content: Raw FB2 file content as bytes

        Returns:
            Document containing the concatenated paragraph text
        """
        logger.info(f"Parsing FB2 document, content size: {len(content)} bytes")

        root = ET.fromstring(content)

        paragraphs = []
        # Find all <body> elements (FB2 may have multiple body sections,
        # e.g. main body and footnotes)
        for body in root.iter(f"{{{_FB2_NS}}}body"):
            for p in body.iter(f"{{{_FB2_NS}}}p"):
                # itertext() collects text from <p> and all nested inline
                # elements (e.g. <emphasis>, <strong>, <a>)
                text = "".join(p.itertext()).strip()
                if text:
                    paragraphs.append(text)

        # Fallback: if namespace-qualified search found nothing, try
        # without namespace (some FB2 files omit the namespace declaration)
        if not paragraphs:
            logger.info("No namespaced paragraphs found, trying without namespace")
            for body in root.iter("body"):
                for p in body.iter("p"):
                    text = "".join(p.itertext()).strip()
                    if text:
                        paragraphs.append(text)

        full_text = "\n".join(paragraphs)
        logger.info(
            f"Successfully parsed FB2, extracted {len(full_text)} characters "
            f"from {len(paragraphs)} paragraphs"
        )
        return Document(content=full_text)
