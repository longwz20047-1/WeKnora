package service

import (
	"os"
	"strings"
	"testing"

	firecrawl "github.com/mendableai/firecrawl-go/v2"
)

// 集成测试：需要 Firecrawl 服务运行
// 运行条件：FIRECRAWL_API_URL 环境变量已设置
func TestFirecrawlScrapeIntegration(t *testing.T) {
	apiURL := os.Getenv("FIRECRAWL_API_URL")
	if apiURL == "" {
		t.Skip("FIRECRAWL_API_URL not set, skipping integration test")
	}

	app, err := firecrawl.NewFirecrawlApp("placeholder", apiURL)
	if err != nil {
		t.Fatalf("failed to create firecrawl app: %v", err)
	}

	result, err := app.ScrapeURL("https://example.com", &firecrawl.ScrapeParams{
		Formats:         []string{"markdown"},
		OnlyMainContent: ptr(true),
		Timeout:         ptr(30000),
	})
	if err != nil {
		t.Fatalf("scrape failed: %v", err)
	}
	if result == nil {
		t.Fatal("scrape returned nil result")
	}
	if !strings.Contains(result.Markdown, "Example") {
		t.Errorf("expected markdown to contain 'Example', got: %s", result.Markdown[:100])
	}
}

func ptr[T any](v T) *T { return &v }
