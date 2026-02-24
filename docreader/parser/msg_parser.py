import logging
import os
import tempfile

import extract_msg

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class MsgParser(BaseParser):
    """
    MSG (Outlook message) parser.

    Uses the ``extract-msg`` library to read Microsoft Outlook ``.msg``
    files.  Extracts subject, sender, date and body text.
    """

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse MSG content by writing bytes to a temp file and
        opening with extract_msg.

        Args:
            content: Raw MSG file content as bytes

        Returns:
            Document containing formatted header block followed by body text
        """
        logger.info(f"Parsing MSG document, content size: {len(content)} bytes")

        tmp_fd, tmp_path = tempfile.mkstemp(suffix=".msg")
        try:
            os.write(tmp_fd, content)
            os.close(tmp_fd)

            msg = extract_msg.Message(tmp_path)
            try:
                subject = msg.subject or ""
                sender = msg.sender or ""
                date = msg.date or ""
                body = (msg.body or "").strip()

                # Build formatted output
                parts = []
                if subject:
                    parts.append(f"Subject: {subject}")
                if sender:
                    parts.append(f"From: {sender}")
                if date:
                    parts.append(f"Date: {date}")

                header_block = "\n".join(parts)
                if header_block and body:
                    full_text = header_block + "\n\n" + body
                else:
                    full_text = header_block or body

                metadata = {}
                if subject:
                    metadata["subject"] = subject
                if sender:
                    metadata["from"] = sender
                if date:
                    metadata["date"] = str(date)

                logger.info(
                    f"Successfully parsed MSG, extracted {len(full_text)} characters"
                )
                return Document(content=full_text, metadata=metadata)
            finally:
                msg.close()
        finally:
            if os.path.exists(tmp_path):
                os.remove(tmp_path)
