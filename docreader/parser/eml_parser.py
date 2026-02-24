import email
import email.policy
import logging

from bs4 import BeautifulSoup

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class EmlParser(BaseParser):
    """
    EML (email message) parser.

    Uses Python's stdlib ``email`` module to parse RFC 2822 messages.
    Extracts headers (From, To, Subject, Date) and body text.  HTML-only
    messages are converted to plain text via BeautifulSoup.
    """

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse EML content by extracting headers and body text.

        Args:
            content: Raw EML file content as bytes

        Returns:
            Document containing formatted header block followed by body text
        """
        logger.info(f"Parsing EML document, content size: {len(content)} bytes")

        msg = email.message_from_bytes(content, policy=email.policy.default)

        # Extract headers
        subject = msg.get("Subject", "")
        from_addr = msg.get("From", "")
        to_addr = msg.get("To", "")
        date = msg.get("Date", "")

        # Extract body
        body_text = self._extract_body(msg)

        # Build formatted output
        parts = []
        if subject:
            parts.append(f"Subject: {subject}")
        if from_addr:
            parts.append(f"From: {from_addr}")
        if to_addr:
            parts.append(f"To: {to_addr}")
        if date:
            parts.append(f"Date: {date}")

        header_block = "\n".join(parts)
        if header_block and body_text:
            full_text = header_block + "\n\n" + body_text
        else:
            full_text = header_block or body_text

        metadata = {}
        if subject:
            metadata["subject"] = subject
        if from_addr:
            metadata["from"] = from_addr
        if date:
            metadata["date"] = date

        logger.info(
            f"Successfully parsed EML, extracted {len(full_text)} characters"
        )
        return Document(content=full_text, metadata=metadata)

    def _extract_body(self, msg: email.message.Message) -> str:
        """
        Walk MIME parts and return concatenated body text.

        Prefers text/plain parts; falls back to text/html (stripped via
        BeautifulSoup) when no plain text part is available.
        """
        plain_parts: list[str] = []
        html_parts: list[str] = []

        if msg.is_multipart():
            for part in msg.walk():
                content_type = part.get_content_type()
                if content_type == "text/plain":
                    payload = part.get_content()
                    if isinstance(payload, str):
                        plain_parts.append(payload.strip())
                elif content_type == "text/html":
                    payload = part.get_content()
                    if isinstance(payload, str):
                        html_parts.append(payload)
        else:
            content_type = msg.get_content_type()
            if content_type == "text/plain":
                payload = msg.get_content()
                if isinstance(payload, str):
                    plain_parts.append(payload.strip())
            elif content_type == "text/html":
                payload = msg.get_content()
                if isinstance(payload, str):
                    html_parts.append(payload)

        # Prefer plain text; fall back to HTML
        if plain_parts:
            return "\n\n".join(plain_parts)

        if html_parts:
            texts = []
            for html in html_parts:
                soup = BeautifulSoup(html, "html.parser")
                texts.append(soup.get_text(separator="\n").strip())
            return "\n\n".join(texts)

        return ""
