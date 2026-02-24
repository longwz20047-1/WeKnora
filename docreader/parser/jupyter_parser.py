"""
Jupyter Notebook Parser Module

This module provides a parser for Jupyter Notebook (.ipynb) files.
It extracts markdown, code, and raw cells into a readable text document.
No extra dependencies are needed beyond the JSON standard library.
"""
import json
import logging
from typing import Any, Dict, List

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class JupyterParser(BaseParser):
    """
    Parser for Jupyter Notebook (.ipynb) files.

    Handles both nbformat v4 (cells at top level) and v3 (cells nested
    under worksheets). Extracts markdown, code, and raw cells into a
    single readable text document.

    Code cells are wrapped in fenced code blocks and their text outputs
    are appended. Markdown and raw cells are included as-is.
    """

    def parse_into_text(self, content: bytes) -> Document:
        """Parse Jupyter Notebook content into a Document.

        Args:
            content: Raw bytes of the .ipynb JSON file.

        Returns:
            Document with the extracted text content and kernel metadata.
        """
        logger.info(
            "Parsing Jupyter Notebook, content size: %d bytes", len(content)
        )

        notebook = json.loads(content)

        # Extract kernel name from metadata
        kernel_name = self._extract_kernel_name(notebook)
        logger.info("Detected kernel: %s", kernel_name)

        # Get cells from either v4 or v3 format
        cells = self._get_cells(notebook)
        logger.info("Found %d cells in notebook", len(cells))

        # Process each cell
        sections: List[str] = []
        for idx, cell in enumerate(cells):
            cell_type = cell.get("cell_type", "")
            source = self._get_source(cell)

            if cell_type == "markdown":
                if source.strip():
                    sections.append(source)
            elif cell_type == "code":
                if source.strip():
                    sections.append(f"```python\n{source}\n```")
                # Also extract text outputs
                output_text = self._extract_outputs(cell)
                if output_text.strip():
                    sections.append(f"Output:\n{output_text}")
            elif cell_type == "raw":
                if source.strip():
                    sections.append(source)
            else:
                logger.debug(
                    "Skipping cell %d with type: %s", idx, cell_type
                )

        full_text = "\n\n".join(sections)
        logger.info(
            "Successfully parsed notebook, extracted %d characters from %d sections",
            len(full_text),
            len(sections),
        )

        return Document(
            content=full_text,
            metadata={"kernel": kernel_name},
        )

    @staticmethod
    def _extract_kernel_name(notebook: Dict[str, Any]) -> str:
        """Extract the kernel display name from notebook metadata.

        Args:
            notebook: Parsed notebook JSON dict.

        Returns:
            Kernel name string, or empty string if not found.
        """
        metadata = notebook.get("metadata", {})
        kernelspec = metadata.get("kernelspec", {})
        return kernelspec.get("display_name", kernelspec.get("name", ""))

    @staticmethod
    def _get_cells(notebook: Dict[str, Any]) -> List[Dict[str, Any]]:
        """Get the list of cells from a notebook, handling both v3 and v4.

        nbformat v4: cells are at notebook["cells"]
        nbformat v3: cells are at notebook["worksheets"][0]["cells"]

        Args:
            notebook: Parsed notebook JSON dict.

        Returns:
            List of cell dicts.
        """
        # Try v4 format first
        if "cells" in notebook:
            return notebook["cells"]

        # Fall back to v3 format
        worksheets = notebook.get("worksheets", [])
        if worksheets:
            return worksheets[0].get("cells", [])

        return []

    @staticmethod
    def _get_source(cell: Dict[str, Any]) -> str:
        """Extract source text from a cell.

        The source field can be either a string or a list of strings.

        Args:
            cell: A single cell dict.

        Returns:
            Source text as a single string.
        """
        source = cell.get("source", "")
        if isinstance(source, list):
            return "".join(source)
        return source

    @staticmethod
    def _extract_outputs(cell: Dict[str, Any]) -> str:
        """Extract text outputs from a code cell.

        Handles stream outputs (stdout/stderr) and execute_result/
        display_data with text/plain content.

        Args:
            cell: A code cell dict.

        Returns:
            Concatenated text output.
        """
        outputs = cell.get("outputs", [])
        text_parts: List[str] = []

        for output in outputs:
            output_type = output.get("output_type", "")

            if output_type == "stream":
                text = output.get("text", "")
                if isinstance(text, list):
                    text = "".join(text)
                if text:
                    text_parts.append(text)

            elif output_type in ("execute_result", "display_data"):
                data = output.get("data", {})
                text = data.get("text/plain", "")
                if isinstance(text, list):
                    text = "".join(text)
                if text:
                    text_parts.append(text)

            elif output_type == "error":
                # Include traceback summary for error outputs
                ename = output.get("ename", "")
                evalue = output.get("evalue", "")
                if ename or evalue:
                    text_parts.append(f"{ename}: {evalue}")

        return "\n".join(text_parts)
