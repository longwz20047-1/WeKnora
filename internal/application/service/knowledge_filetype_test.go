package service

import (
	"strings"
	"testing"
)

func TestGetFileTypeNew(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"report.pdf", "pdf"},
		{"code.py", "py"},
		{"style.CSS", "css"},
		{"archive.tar.gz", "gz"},
		{".gitignore", "gitignore"},
		{".editorconfig", "editorconfig"},
		{"Makefile", "makefile"},
		{"Dockerfile", "dockerfile"},
		{"DOCKERFILE", "dockerfile"},
		{"Gemfile", "gemfile"},
		{"Rakefile", "rakefile"},
		{"CMakeLists.txt", "cmake"},
		{"README", "unknown"},
		{"noext", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := getFileTypeNew(tt.filename)
			if result != tt.expected {
				t.Errorf("getFileTypeNew(%q) = %q, want %q", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestIsValidFileTypeNew(t *testing.T) {
	validFiles := []string{
		"code.py", "config.yaml", "script.sh", "style.css", "data.json",
		"Makefile",
	}
	for _, f := range validFiles {
		if !isValidFileTypeNew(f) {
			t.Errorf("isValidFileTypeNew(%q) should be true", f)
		}
	}

	fullParseFiles := []string{"doc.pdf", "text.txt", "image.jpg", "data.csv"}
	for _, f := range fullParseFiles {
		if !isValidFileTypeNew(f) {
			t.Errorf("isValidFileTypeNew(%q) should be true", f)
		}
	}

	rejectedFiles := []string{"model.dwg", "app.exe", "lib.dll", "noext", "unknown.zzz"}
	for _, f := range rejectedFiles {
		if isValidFileTypeNew(f) {
			t.Errorf("isValidFileTypeNew(%q) should be false", f)
		}
	}
}

func TestGetFileProcessStrategy(t *testing.T) {
	tests := []struct {
		fileType string
		expected string
	}{
		{"pdf", FileProcessFullParse},
		{"txt", FileProcessFullParse},
		{"jpg", FileProcessFullParse},
		{"py", FileProcessTextAsIs},
		{"yaml", FileProcessTextAsIs},
		{"sh", FileProcessTextAsIs},
		{"pptx", FileProcessConvertParse},
		{"rtf", FileProcessConvertParse},
		{"stl", FileProcessStorePreview},
		{"dxf", FileProcessStorePreview},
		{"exe", ""},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.fileType, func(t *testing.T) {
			result := getFileProcessStrategy(tt.fileType)
			if result != tt.expected {
				t.Errorf("getFileProcessStrategy(%q) = %q, want %q", tt.fileType, result, tt.expected)
			}
		})
	}
}

func TestGetFileSizeLimit(t *testing.T) {
	tests := []struct {
		fileType string
		expected int64
	}{
		{"psd", 500 * 1024 * 1024},
		{"epub", 200 * 1024 * 1024},
		{"chm", 100 * 1024 * 1024},
		{"jpg", 20 * 1024 * 1024},
		{"jpeg", 20 * 1024 * 1024},
		{"png", 20 * 1024 * 1024},
		{"gif", 20 * 1024 * 1024},
		{"bmp", 20 * 1024 * 1024},
		{"tiff", 20 * 1024 * 1024},
		{"webp", 20 * 1024 * 1024},
		{"pdf", 50 * 1024 * 1024},
		{"py", 10 * 1024 * 1024},
		{"pptx", 100 * 1024 * 1024},
		{"stl", 200 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.fileType, func(t *testing.T) {
			limit := getFileSizeLimit(tt.fileType)
			if limit != tt.expected {
				t.Errorf("getFileSizeLimit(%q) = %d, want %d", tt.fileType, limit, tt.expected)
			}
		})
	}
}

func TestValidateFileSize(t *testing.T) {
	// Within limit - should return nil.
	if err := validateFileSize("pdf", 1*1024*1024); err != nil {
		t.Errorf("validateFileSize(pdf, 1MB) should be nil, got %v", err)
	}

	// Exactly at limit - should return nil.
	if err := validateFileSize("py", 10*1024*1024); err != nil {
		t.Errorf("validateFileSize(py, 10MB) should be nil, got %v", err)
	}

	// Over limit - should return error with machine-parseable format.
	err := validateFileSize("py", 11*1024*1024)
	if err == nil {
		t.Fatal("validateFileSize(py, 11MB) should return an error")
	}

	errMsg := err.Error()
	if !strings.HasPrefix(errMsg, "FILE_TOO_LARGE:") {
		t.Errorf("error should start with FILE_TOO_LARGE:, got %q", errMsg)
	}

	expected := "FILE_TOO_LARGE:py:11534336:10485760"
	if errMsg != expected {
		t.Errorf("error = %q, want %q", errMsg, expected)
	}

	// Type-specific override: jpg has 20MB limit.
	err = validateFileSize("jpg", 21*1024*1024)
	if err == nil {
		t.Fatal("validateFileSize(jpg, 21MB) should return an error")
	}
	if !strings.HasPrefix(err.Error(), "FILE_TOO_LARGE:jpg:") {
		t.Errorf("error should reference jpg, got %q", err.Error())
	}
}
