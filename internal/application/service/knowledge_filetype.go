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
	// Documents
	"pdf": true, "txt": true, "md": true, "markdown": true,
	"docx": true, "doc": true, "xlsx": true, "xls": true,
	// Images
	"png": true, "jpg": true, "jpeg": true, "gif": true,
	"bmp": true, "webp": true, "svg": true, "tiff": true, "tif": true,
	// Data
	"csv": true, "tsv": true, "json": true, "jsonl": true,
	"xml": true, "html": true, "htm": true,
}

// convertParseTypes need conversion (e.g. Calibre) before parsing.
var convertParseTypes = map[string]bool{
	// Presentations
	"pptx": true, "ppt": true, "odp": true,
	// Rich text / legacy office
	"rtf": true, "odt": true, "ods": true,
	// E-books
	"epub": true, "mobi": true, "azw3": true, "fb2": true,
	// LaTeX
	"tex": true, "latex": true,
}

// textAsIsTypes are plain-text files ingested verbatim.
var textAsIsTypes = map[string]bool{
	// Programming languages
	"py": true, "js": true, "ts": true, "jsx": true, "tsx": true,
	"go": true, "rs": true, "java": true, "kt": true, "kts": true,
	"c": true, "h": true, "cpp": true, "hpp": true, "cc": true, "cxx": true,
	"cs": true, "swift": true, "m": true, "mm": true,
	"rb": true, "php": true, "pl": true, "pm": true, "lua": true,
	"r": true, "jl": true, "scala": true, "clj": true, "ex": true, "exs": true,
	"erl": true, "hrl": true, "hs": true, "elm": true, "dart": true,
	"v": true, "vhdl": true, "vhd": true,
	// Shell / scripting
	"sh": true, "bash": true, "zsh": true, "fish": true, "ps1": true,
	"bat": true, "cmd": true,
	// Web
	"css": true, "scss": true, "sass": true, "less": true,
	"vue": true, "svelte": true,
	// Config / data
	"yaml": true, "yml": true, "toml": true, "ini": true, "cfg": true,
	"conf": true, "properties": true, "env": true,
	// Build / CI
	"cmake": true, "gradle": true, "sbt": true,
	// Markup / docs
	"rst": true, "adoc": true, "asciidoc": true, "org": true, "textile": true,
	// SQL / query
	"sql": true, "graphql": true, "gql": true,
	// Proto / schema
	"proto": true, "avro": true, "thrift": true,
	// Other text
	"log": true, "diff": true, "patch": true,
	"gitignore": true, "dockerignore": true, "editorconfig": true,
}

// storePreviewTypes are binary files stored for download & optional preview.
var storePreviewTypes = map[string]bool{
	// CAD / 3D
	"stl": true, "obj": true, "step": true, "stp": true, "iges": true, "igs": true,
	"dxf": true, "3mf": true, "fbx": true, "gltf": true, "glb": true,
	// Audio
	"mp3": true, "wav": true, "flac": true, "aac": true, "ogg": true, "wma": true,
	// Video
	"mp4": true, "avi": true, "mkv": true, "mov": true, "wmv": true, "flv": true, "webm": true,
	// Archives
	"zip": true, "tar": true, "gz": true, "bz2": true, "xz": true, "7z": true, "rar": true,
	// Design
	"psd": true, "ai": true, "sketch": true, "fig": true, "xd": true,
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
	// Images are usually small; cap at 20 MB.
	"png": 20 * 1024 * 1024, "jpg": 20 * 1024 * 1024, "jpeg": 20 * 1024 * 1024,
	"gif": 20 * 1024 * 1024, "bmp": 20 * 1024 * 1024, "webp": 20 * 1024 * 1024,
	"svg": 20 * 1024 * 1024, "tiff": 20 * 1024 * 1024, "tif": 20 * 1024 * 1024,
}

// calibreAvailable is set during init() when the calibre ebook-convert CLI is
// found on PATH. When false, convertParseTypes that require Calibre are not
// registered.
var calibreAvailable bool

func init() {
	_, err := exec.LookPath("ebook-convert")
	calibreAvailable = err == nil
}

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
	if textAsIsTypes[fileType] {
		return FileProcessTextAsIs
	}
	if convertParseTypes[fileType] {
		return FileProcessConvertParse
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
// for the file type.  Returns nil when acceptable, or a descriptive error.
func validateFileSize(fileType string, size int64) error {
	limit := getFileSizeLimit(fileType)
	if size > limit {
		return fmt.Errorf("file size %d bytes exceeds limit %d bytes for type %q", size, limit, fileType)
	}
	return nil
}
