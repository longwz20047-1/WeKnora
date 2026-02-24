"""Tests for JupyterParser - extracts text from .ipynb notebook files."""
import json

import pytest
from unittest.mock import patch

from docreader.models.document import Document


def _make_v4_notebook(cells, kernel_name="Python 3"):
    """Helper: build a minimal nbformat v4 notebook dict."""
    return {
        "nbformat": 4,
        "nbformat_minor": 5,
        "metadata": {
            "kernelspec": {
                "display_name": kernel_name,
                "name": "python3",
            }
        },
        "cells": cells,
    }


def _make_v3_notebook(cells, kernel_name="Python 3"):
    """Helper: build a minimal nbformat v3 notebook dict."""
    return {
        "nbformat": 3,
        "nbformat_minor": 0,
        "metadata": {
            "kernelspec": {
                "display_name": kernel_name,
                "name": "python3",
            }
        },
        "worksheets": [{"cells": cells}],
    }


def _to_bytes(notebook_dict):
    """Serialize a notebook dict to bytes."""
    return json.dumps(notebook_dict).encode("utf-8")


class TestJupyterParser:
    """Tests for JupyterParser - parses .ipynb Jupyter Notebook files."""

    def setup_method(self):
        """Set up test fixtures with mocked BaseParser dependencies."""
        with patch("docreader.parser.base_parser.create_storage"), \
             patch("docreader.parser.base_parser.Caption", return_value=None):
            from docreader.parser.jupyter_parser import JupyterParser
            self.parser = JupyterParser(
                file_name="test.ipynb",
                file_type="ipynb",
                chunk_size=500,
                chunk_overlap=50,
                enable_multimodal=False,
            )

    def test_returns_document(self):
        """parse_into_text returns a Document instance."""
        nb = _make_v4_notebook([])
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert isinstance(result, Document)

    def test_empty_notebook(self):
        """Empty notebook produces empty content."""
        nb = _make_v4_notebook([])
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert result.content == ""

    def test_markdown_cell(self):
        """Markdown cells are included as-is."""
        cells = [
            {"cell_type": "markdown", "source": "# Hello World\n\nSome text.", "metadata": {}},
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "# Hello World" in result.content
        assert "Some text." in result.content

    def test_code_cell_wrapped_in_code_block(self):
        """Code cells are wrapped in ```python fenced code blocks."""
        cells = [
            {
                "cell_type": "code",
                "source": "print('hello')",
                "metadata": {},
                "outputs": [],
            },
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "```python\nprint('hello')\n```" in result.content

    def test_code_cell_with_stream_output(self):
        """Code cell outputs (stream) are included."""
        cells = [
            {
                "cell_type": "code",
                "source": "print('hello')",
                "metadata": {},
                "outputs": [
                    {"output_type": "stream", "name": "stdout", "text": "hello\n"},
                ],
            },
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "hello" in result.content
        assert "Output:" in result.content

    def test_code_cell_with_execute_result(self):
        """Code cell execute_result outputs are included."""
        cells = [
            {
                "cell_type": "code",
                "source": "42",
                "metadata": {},
                "outputs": [
                    {
                        "output_type": "execute_result",
                        "data": {"text/plain": "42"},
                        "metadata": {},
                        "execution_count": 1,
                    },
                ],
            },
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "42" in result.content

    def test_raw_cell(self):
        """Raw cells are included as-is."""
        cells = [
            {"cell_type": "raw", "source": "raw content here", "metadata": {}},
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "raw content here" in result.content

    def test_unknown_cell_type_skipped(self):
        """Unknown cell types are silently skipped."""
        cells = [
            {"cell_type": "heading", "source": "skip me", "metadata": {}},
            {"cell_type": "markdown", "source": "keep me", "metadata": {}},
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "skip me" not in result.content
        assert "keep me" in result.content

    def test_source_as_list(self):
        """Source provided as a list of strings is joined correctly."""
        cells = [
            {
                "cell_type": "code",
                "source": ["import os\n", "print(os.getcwd())"],
                "metadata": {},
                "outputs": [],
            },
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "import os\nprint(os.getcwd())" in result.content

    def test_v3_format(self):
        """Handles nbformat v3 (cells under worksheets)."""
        cells = [
            {"cell_type": "markdown", "source": "v3 notebook", "metadata": {}},
            {
                "cell_type": "code",
                "source": "x = 1",
                "metadata": {},
                "outputs": [],
            },
        ]
        nb = _make_v3_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "v3 notebook" in result.content
        assert "```python\nx = 1\n```" in result.content

    def test_mixed_cells(self):
        """Multiple cell types are separated by double newlines."""
        cells = [
            {"cell_type": "markdown", "source": "# Title", "metadata": {}},
            {
                "cell_type": "code",
                "source": "x = 1",
                "metadata": {},
                "outputs": [],
            },
            {"cell_type": "raw", "source": "raw data", "metadata": {}},
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "# Title" in result.content
        assert "```python\nx = 1\n```" in result.content
        assert "raw data" in result.content
        # Sections are joined with \n\n
        assert "\n\n" in result.content

    def test_kernel_metadata(self):
        """Kernel name is stored in metadata."""
        nb = _make_v4_notebook([], kernel_name="Julia 1.9")
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert result.metadata["kernel"] == "Julia 1.9"

    def test_error_output(self):
        """Error outputs include ename and evalue."""
        cells = [
            {
                "cell_type": "code",
                "source": "1/0",
                "metadata": {},
                "outputs": [
                    {
                        "output_type": "error",
                        "ename": "ZeroDivisionError",
                        "evalue": "division by zero",
                        "traceback": [],
                    },
                ],
            },
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "ZeroDivisionError: division by zero" in result.content

    def test_output_text_as_list(self):
        """Output text provided as a list of strings is joined."""
        cells = [
            {
                "cell_type": "code",
                "source": "print('a'); print('b')",
                "metadata": {},
                "outputs": [
                    {
                        "output_type": "stream",
                        "name": "stdout",
                        "text": ["a\n", "b\n"],
                    },
                ],
            },
        ]
        nb = _make_v4_notebook(cells)
        result = self.parser.parse_into_text(_to_bytes(nb))
        assert "a\nb\n" in result.content
