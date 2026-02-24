import email
import email.policy
import logging

from bs4 import BeautifulSoup

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class MhtmlParser(BaseParser):
    """
    MHTML (MIME HTML / web archive) parser.

    MHTML files are MIME multipart messages (RFC 2557).  This parser uses
    Python's stdlib ``email`` module to walk the MIME parts, finds the
    ``text/html`` content, decodes it (handling base64 / quoted-printable
    transfer encodings), and strips HTML tags with BeautifulSoup.
    """

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse MHTML content by extracting and stripping HTML parts.

        Args:
            content: Raw MHTML file content as bytes

        Returns:
            Document containing the extracted plain text
        """
        logger.info(f"Parsing MHTML document, content size: {len(content)} bytes")

        msg = email.message_from_bytes(content, policy=email.policy.default)

        html_texts: list[str] = []
        plain_texts: list[str] = []

        for part in msg.walk():
            content_type = part.get_content_type()
            if content_type == "text/html":
                payload = part.get_content()
                if isinstance(payload, str) and payload.strip():
                    html_texts.append(payload)
            elif content_type == "text/plain":
                payload = part.get_content()
                if isinstance(payload, str) and payload.strip():
                    plain_texts.append(payload.strip())

        # Prefer HTML content (the primary purpose of MHTML); fall back
        # to plain text parts if no HTML was found.
        if html_texts:
            texts = []
            for html in html_texts:
                soup = BeautifulSoup(html, "html.parser")
                text = soup.get_text(separator="\n").strip()
                if text:
                    texts.append(text)
            full_text = "\n\n".join(texts)
        elif plain_texts:
            full_text = "\n\n".join(plain_texts)
        else:
            full_text = ""

        logger.info(
            f"Successfully parsed MHTML, extracted {len(full_text)} characters"
        )
        return Document(content=full_text)
