package handler

import (
	"testing"
)

func TestAnalyzeHTMLDetection(t *testing.T) {
	tests := []struct {
		name           string
		html           string
		wantPageType   string
		wantRecommend  string
	}{
		{
			name:          "cloudflare block",
			html:          `<html><body><div class="cf-browser-verification">please wait</div></body></html>`,
			wantPageType:  "login_required",
			wantRecommend: "manual",
		},
		{
			name:          "login form",
			html:          `<html><body><form><input type="password" name="pwd"/></form></body></html>`,
			wantPageType:  "login_required",
			wantRecommend: "manual",
		},
		{
			name:          "normal page",
			html:          `<html><head><title>Hello</title></head><body><p>content</p></body></html>`,
			wantPageType:  "static_html",
			wantRecommend: "auto",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzeHTMLContent([]byte(tt.html))
			if got.PageType != tt.wantPageType {
				t.Errorf("PageType = %q, want %q", got.PageType, tt.wantPageType)
			}
			if got.Recommendation != tt.wantRecommend {
				t.Errorf("Recommendation = %q, want %q", got.Recommendation, tt.wantRecommend)
			}
		})
	}
}

func TestIsInternalURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{"loopback IPv4", "http://127.0.0.1/path", true},
		{"loopback localhost", "http://localhost/path", true},
		{"private 10.x", "http://10.0.0.1/path", true},
		{"private 172.16.x", "http://172.16.0.1/path", true},
		{"private 192.168.x", "http://192.168.1.1/path", true},
		{"public IP", "http://8.8.8.8/path", false},
		{"public domain", "https://example.com", false},
		{"non-http scheme", "ftp://example.com", true},
		{"empty string", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInternalURL(tt.url)
			if got != tt.expected {
				t.Errorf("isInternalURL(%q) = %v, want %v", tt.url, got, tt.expected)
			}
		})
	}
}
