package service

import (
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
	validFiles := []string{"code.py", "config.yaml", "script.sh", "style.css", "data.json"}
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
	if limit := getFileSizeLimit("jpg"); limit != 20*1024*1024 {
		t.Errorf("jpg limit = %d, want %d", limit, 20*1024*1024)
	}
	if limit := getFileSizeLimit("py"); limit != 10*1024*1024 {
		t.Errorf("py limit = %d, want %d", limit, 10*1024*1024)
	}
	if limit := getFileSizeLimit("pdf"); limit != 50*1024*1024 {
		t.Errorf("pdf limit = %d, want %d", limit, 50*1024*1024)
	}
	if limit := getFileSizeLimit("pptx"); limit != 100*1024*1024 {
		t.Errorf("pptx limit = %d, want %d", limit, 100*1024*1024)
	}
}
