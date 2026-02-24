import pytest
from unittest.mock import patch, MagicMock

from docreader.models.document import Document


class TestMsgParser:
    """Tests for MsgParser - extracts text from Outlook MSG files."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.msg_parser import MsgParser
            self.parser = MsgParser(
                file_name="test.msg",
                file_type="msg",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    @patch("docreader.parser.msg_parser.tempfile")
    @patch("docreader.parser.msg_parser.os")
    def test_extracts_subject_sender_body(self, mock_os, mock_tempfile):
        """Extracts subject, sender, date and body from MSG file."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.msg")
        mock_os.path.exists.return_value = True

        mock_msg = MagicMock()
        mock_msg.subject = "Meeting Notes"
        mock_msg.sender = "alice@example.com"
        mock_msg.date = "2024-01-15 10:00:00"
        mock_msg.body = "Please review the attached notes."

        with patch("docreader.parser.msg_parser.extract_msg") as mock_extract:
            mock_extract.Message.return_value = mock_msg

            result = self.parser.parse_into_text(b"fake msg bytes")

        assert isinstance(result, Document)
        assert "Subject: Meeting Notes" in result.content
        assert "From: alice@example.com" in result.content
        assert "Please review the attached notes." in result.content
        assert result.metadata["subject"] == "Meeting Notes"
        assert result.metadata["from"] == "alice@example.com"

    @patch("docreader.parser.msg_parser.tempfile")
    @patch("docreader.parser.msg_parser.os")
    def test_handles_empty_body(self, mock_os, mock_tempfile):
        """MSG with no body text returns headers only."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.msg")
        mock_os.path.exists.return_value = True

        mock_msg = MagicMock()
        mock_msg.subject = "Empty"
        mock_msg.sender = "bob@example.com"
        mock_msg.date = "2024-01-15"
        mock_msg.body = ""

        with patch("docreader.parser.msg_parser.extract_msg") as mock_extract:
            mock_extract.Message.return_value = mock_msg

            result = self.parser.parse_into_text(b"fake msg bytes")

        assert "Subject: Empty" in result.content
        # No body text appended
        assert "\n\n" not in result.content or result.content.endswith("Empty")

    @patch("docreader.parser.msg_parser.tempfile")
    @patch("docreader.parser.msg_parser.os")
    def test_handles_none_fields(self, mock_os, mock_tempfile):
        """MSG with None fields does not crash."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.msg")
        mock_os.path.exists.return_value = True

        mock_msg = MagicMock()
        mock_msg.subject = None
        mock_msg.sender = None
        mock_msg.date = None
        mock_msg.body = None

        with patch("docreader.parser.msg_parser.extract_msg") as mock_extract:
            mock_extract.Message.return_value = mock_msg

            result = self.parser.parse_into_text(b"fake msg bytes")

        assert isinstance(result, Document)
        assert result.content == ""

    @patch("docreader.parser.msg_parser.tempfile")
    @patch("docreader.parser.msg_parser.os")
    def test_cleans_up_temp_file(self, mock_os, mock_tempfile):
        """Temporary file is removed after parsing."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.msg")
        mock_os.path.exists.return_value = True

        mock_msg = MagicMock()
        mock_msg.subject = "Test"
        mock_msg.sender = "a@b.com"
        mock_msg.date = ""
        mock_msg.body = "body"

        with patch("docreader.parser.msg_parser.extract_msg") as mock_extract:
            mock_extract.Message.return_value = mock_msg

            self.parser.parse_into_text(b"fake msg")

        mock_os.remove.assert_called_once_with("/tmp/fake.msg")

    @patch("docreader.parser.msg_parser.tempfile")
    @patch("docreader.parser.msg_parser.os")
    def test_cleans_up_temp_file_on_error(self, mock_os, mock_tempfile):
        """Temporary file is removed even when extract_msg fails."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.msg")
        mock_os.path.exists.return_value = True

        with patch("docreader.parser.msg_parser.extract_msg") as mock_extract:
            mock_extract.Message.side_effect = Exception("corrupt msg")

            with pytest.raises(Exception, match="corrupt msg"):
                self.parser.parse_into_text(b"bad data")

        mock_os.remove.assert_called_once_with("/tmp/fake.msg")

    @patch("docreader.parser.msg_parser.tempfile")
    @patch("docreader.parser.msg_parser.os")
    def test_closes_msg_handle(self, mock_os, mock_tempfile):
        """The extract_msg.Message handle is always closed."""
        mock_tempfile.mkstemp.return_value = (99, "/tmp/fake.msg")
        mock_os.path.exists.return_value = True

        mock_msg = MagicMock()
        mock_msg.subject = "Test"
        mock_msg.sender = ""
        mock_msg.date = ""
        mock_msg.body = "text"

        with patch("docreader.parser.msg_parser.extract_msg") as mock_extract:
            mock_extract.Message.return_value = mock_msg

            self.parser.parse_into_text(b"msg bytes")

        mock_msg.close.assert_called_once()
