"""Test runner for docreader parser tests.

This script installs a mock import hook before any docreader code is loaded,
allowing tests to run without the full set of heavy ML/document dependencies.

Usage:
    cd d:/workspace/WeKnora
    PYTHONPATH=. python run_parser_tests.py              # run all parser tests
    PYTHONPATH=. python run_parser_tests.py -k converts  # run specific tests
"""
import importlib.abc
import importlib.machinery
import sys
from unittest.mock import MagicMock

# Modules (top-level) that should be AUTO-MOCKED.
# These are heavy third-party libraries that may not be installed locally.
_MOCK_MODULES = frozenset({
    "textract",
    "paddleocr", "paddlepaddle", "paddle",
    "goose3",
    "trafilatura", "courlan", "htmldate",
    "markitdown",
    "playwright",
    "ollama",
    "openai",
    "pdfplumber",
    "pypdf", "pypdf2", "PyPDF2",
    "docx",
    "mistletoe",
    "markdown",
    "markdownify",
    "bs4", "beautifulsoup4",
    "lxml",
    "antiword",
    "openpyxl", "xlrd",
    "grpc", "grpcio",
    "google",
})


class _MockFinder(importlib.abc.MetaPathFinder):
    """Meta-path finder that mocks only known heavy third-party modules."""

    def find_spec(self, fullname, path, target=None):
        top = fullname.split(".")[0]
        if top in _MOCK_MODULES:
            return importlib.machinery.ModuleSpec(
                fullname, _MockLoader(), is_package=True
            )
        return None


class _MockLoader(importlib.abc.Loader):
    """Loader that creates MagicMock modules."""

    def create_module(self, spec):
        mock = MagicMock()
        mock.__name__ = spec.name
        mock.__path__ = []
        mock.__file__ = spec.name
        mock.__loader__ = self
        mock.__spec__ = spec
        mock.__package__ = spec.name
        return mock

    def exec_module(self, module):
        pass


# Install BEFORE anything else is imported
sys.meta_path.insert(0, _MockFinder())

if __name__ == "__main__":
    import pytest

    sys.exit(
        pytest.main(
            [
                "-v",
                "--rootdir", ".",
                "-c", "NUL",  # Windows null device, skip pyproject.toml
                "--noconftest",  # skip conftest.py to avoid package __init__ imports
                "--import-mode=importlib",
                "docreader/parser/tests/test_libreoffice_parser.py",
            ]
            + sys.argv[1:]
        )
    )
