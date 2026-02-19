package handler

import (
	"strings"
	"testing"
)

func TestHtmlToMarkdown(t *testing.T) {
	tests := []struct {
		name         string
		html         string
		pageURL      string
		expectTitle  string
		expectSubstr string
	}{
		{
			name:         "simple article",
			html:         `<html><head><title>Test</title></head><body><article><h1>Hello</h1><p>World</p></article></body></html>`,
			pageURL:      "https://example.com/page",
			expectTitle:  "Test",
			expectSubstr: "Hello",
		},
		{
			name:         "readability fallback",
			html:         `<p>Just text</p>`,
			pageURL:      "https://example.com",
			expectTitle:  "",
			expectSubstr: "Just text",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md, title, err := htmlToMarkdown(tt.html, tt.pageURL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.expectTitle != "" && title != tt.expectTitle {
				t.Errorf("title = %q, want %q", title, tt.expectTitle)
			}
			if !strings.Contains(md, tt.expectSubstr) {
				t.Errorf("markdown doesn't contain %q, got: %s", tt.expectSubstr, md)
			}
		})
	}
}
