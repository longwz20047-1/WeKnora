package service

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// File processing strategy constants.
const (
	// FileProcessFullParse: files parsed natively (PDF, images, text, CSV, Office docs).
	FileProcessFullParse = "full_parse"
	// FileProcessConvertParse: files converted first (e.g. via Calibre), then parsed.
	FileProcessConvertParse = "convert_parse"
	// FileProcessTextAsIs: plain-text source/config files ingested verbatim.
	FileProcessTextAsIs = "text_as_is"
	// FileProcessStorePreview: binary/CAD files stored for download & preview only.
	FileProcessStorePreview = "store_preview"
)

// Default file size limits per strategy (bytes).
const (
	defaultFullParseLimit    = 50 * 1024 * 1024  // 50 MB
	defaultConvertParseLimit = 100 * 1024 * 1024 // 100 MB
	defaultTextAsIsLimit     = 10 * 1024 * 1024  // 10 MB
	defaultStorePreviewLimit = 200 * 1024 * 1024 // 200 MB
)

// fullParseTypes are file types that can be parsed natively by the platform.
var fullParseTypes = map[string]bool{
	"pdf": true, "docx": true, "doc": true,
	"md": true, "markdown": true, "txt": true,
	"csv": true, "xlsx": true, "xls": true,
	"jpg": true, "jpeg": true, "png": true, "gif": true,
	"bmp": true, "tiff": true, "webp": true,
	// Email/Note/Archive formats
	"eml": true, "msg": true, "enex": true, "mht": true, "mhtml": true,
	// Jupyter Notebook
	"ipynb": true,
}

// convertParseTypes need conversion (e.g. Calibre) before parsing.
var convertParseTypes = map[string]bool{
	"pptx": true, "ppt": true, "pptm": true, "potx": true, "potm": true,
	"rtf": true, "odt": true, "ods": true, "odp": true,
	"wps": true, "docm": true, "dotx": true, "dotm": true,
	"xlsm": true, "xltx": true, "xltm": true,
	"pages": true, "numbers": true, "key": true,
	"vsdx": true, "vsd": true, "pub": true,
	"hwp": true, "hwpx": true,
}

// textAsIsTypes are plain-text files ingested verbatim.
var textAsIsTypes = map[string]bool{
	// 代码
	"py": true, "java": true, "go": true, "ts": true, "js": true,
	"jsx": true, "tsx": true, "vue": true, "c": true, "cpp": true,
	"h": true, "hpp": true, "cs": true, "rb": true, "rs": true,
	"php": true, "swift": true, "kt": true, "scala": true, "r": true,
	"lua": true, "pl": true, "dart": true,
	"zig": true, "nim": true, "asm": true, "s": true,
	"hs": true, "ex": true, "exs": true, "erl": true,
	"ml": true, "mli": true, "fs": true, "fsx": true,
	"clj": true, "cljs": true, "groovy": true,
	"v": true, "vhdl": true, "vhd": true,
	"sol": true,
	// 配置/数据
	"json": true, "xml": true, "yaml": true, "yml": true, "toml": true,
	"ini": true, "conf": true, "cfg": true, "env": true, "properties": true,
	"gradle": true, "jsonl": true, "ndjson": true, "tsv": true,
	// 脚本
	"sh": true, "bash": true, "zsh": true, "bat": true, "cmd": true,
	"ps1": true, "psm1": true,
	// 文档标记
	"html": true, "htm": true, "css": true, "less": true, "scss": true,
	"sass": true, "svg": true, "tex": true, "latex": true,
	"rst": true, "adoc": true, "org": true,
	"rmd": true, "qmd": true, "opml": true,
	// 字幕
	"srt": true, "vtt": true, "ass": true, "ssa": true, "sub": true,
	// 引用/学术
	"bib": true, "ris": true, "csl": true,
	// 日历/通讯录
	"ics": true, "vcf": true,
	// 其他文本
	"log": true, "sql": true, "graphql": true, "proto": true, "thrift": true,
	"makefile": true, "dockerfile": true, "gemfile": true, "rakefile": true, "cmake": true,
	"gitignore": true, "editorconfig": true,
	"dockerignore": true, "prettierrc": true, "eslintrc": true, "babelrc": true,
	"npmrc": true, "yarnrc": true, "stylelintrc": true,
}

// storePreviewTypes are binary files stored for download & optional preview.
var storePreviewTypes = map[string]bool{
	"stl": true, "obj": true, "fbx": true, "gltf": true, "glb": true,
	"3ds": true, "dae": true, "ply": true, "dxf": true, "psd": true,
}

// specialFileNames maps well-known filenames (case-insensitive) to a
// canonical file type string.
var specialFileNames = map[string]string{
	"makefile":       "makefile",
	"dockerfile":     "dockerfile",
	"gemfile":        "gemfile",
	"rakefile":       "rakefile",
	"cmakelists.txt": "cmake",
}

// fileSizeLimits maps strategy -> default byte limit.
var fileSizeLimits = map[string]int64{
	FileProcessFullParse:    defaultFullParseLimit,
	FileProcessConvertParse: defaultConvertParseLimit,
	FileProcessTextAsIs:     defaultTextAsIsLimit,
	FileProcessStorePreview: defaultStorePreviewLimit,
}

// fileTypeSizeOverrides provides per-type size overrides (bytes).
var fileTypeSizeOverrides = map[string]int64{
	"psd": 500 * 1024 * 1024,
	// Pre-provisioned for Phase 3 (dedicated EPUB/CHM parsers)
	"epub": 200 * 1024 * 1024, // 200MB
	"chm":  100 * 1024 * 1024, // 100MB
	"jpg":  20 * 1024 * 1024, "jpeg": 20 * 1024 * 1024,
	"png":  20 * 1024 * 1024, "gif": 20 * 1024 * 1024,
	"bmp":  20 * 1024 * 1024, "tiff": 20 * 1024 * 1024,
	"webp": 20 * 1024 * 1024,
}

func init() {
	if _, err := exec.LookPath("ebook-convert"); err == nil {
		for _, ext := range []string{"azw3", "azw", "prc", "mobi"} {
			fullParseTypes[ext] = true
		}
	}
}

// After init(), all type maps should be treated as read-only.

// ---------------------------------------------------------------------------
// Public / package-level helpers
// ---------------------------------------------------------------------------

// getFileTypeNew extracts a normalised file type from a filename.
//
// Rules (evaluated in order):
//  1. Special filenames (Makefile, Dockerfile, ...) -> mapped type.
//  2. Dotfiles with no other extension (.gitignore) -> name without dot.
//  3. Regular extensions (report.pdf, archive.tar.gz) -> last extension.
//  4. Otherwise -> "unknown".
func getFileTypeNew(filename string) string {
	base := filepath.Base(filename)

	// 1. Special filenames (case-insensitive).
	if mapped, ok := specialFileNames[strings.ToLower(base)]; ok {
		return mapped
	}

	// 2. Use filepath.Ext for the extension (includes the dot).
	ext := filepath.Ext(base)
	if ext == "" {
		// Could be a dotfile like ".gitignore".
		if strings.HasPrefix(base, ".") && len(base) > 1 {
			return strings.ToLower(base[1:])
		}
		return "unknown"
	}

	// 3. Normalise: strip leading dot, lowercase.
	return strings.ToLower(ext[1:])
}

// isValidFileTypeNew returns true when the file can be processed by at least
// one of the four strategies.
func isValidFileTypeNew(filename string) bool {
	ft := getFileTypeNew(filename)
	if ft == "unknown" {
		return false
	}
	return fullParseTypes[ft] || convertParseTypes[ft] || textAsIsTypes[ft] || storePreviewTypes[ft]
}

// getFileProcessStrategy returns the processing strategy constant for a given
// (already-normalised) file type.  Returns empty string for unknown types.
func getFileProcessStrategy(fileType string) string {
	if fullParseTypes[fileType] {
		return FileProcessFullParse
	}
	if convertParseTypes[fileType] {
		return FileProcessConvertParse
	}
	if textAsIsTypes[fileType] {
		return FileProcessTextAsIs
	}
	if storePreviewTypes[fileType] {
		return FileProcessStorePreview
	}
	return ""
}

// getFileSizeLimit returns the maximum allowed upload size (bytes) for the
// given file type.  Type-specific overrides take precedence over strategy
// defaults.
func getFileSizeLimit(fileType string) int64 {
	if override, ok := fileTypeSizeOverrides[fileType]; ok {
		return override
	}
	strategy := getFileProcessStrategy(fileType)
	if limit, ok := fileSizeLimits[strategy]; ok {
		return limit
	}
	return defaultFullParseLimit // safe fallback
}

// validateFileSize checks whether the given size (bytes) is within the limit
// for the file type.  Returns nil when acceptable, or a machine-parseable error.
func validateFileSize(fileType string, size int64) error {
	limit := getFileSizeLimit(fileType)
	if size > limit {
		return fmt.Errorf("FILE_TOO_LARGE:%s:%d:%d", fileType, size, limit)
	}
	return nil
}
