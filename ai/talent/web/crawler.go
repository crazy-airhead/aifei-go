// crawler.go 远程爬虫驱动能力包（对标 WebCrawlerDriverTalent：JINA r.jina.ai
// 与 Firecrawl api.firecrawl.dev 两驱动——网页转 Markdown 由远端服务完成）。
// 纯工具集形态（无指令注入、无准入门槛）。
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// CrawlerDriver 爬虫驱动 SPI。
type CrawlerDriver interface {
	// Crawl 抓取 URL 并转 Markdown。
	Crawl(ctx context.Context, url, apiKey string) (string, error)
}

// JinaCrawlerDriver JINA 驱动（GET https://r.jina.ai/{url}，浏览器头 +
// X-Return-Format: markdown；apiKey 非空时 Bearer）。
type JinaCrawlerDriver struct{ Doer httpx.Doer }

// NewJinaCrawlerDriver 构造（Doer 可注入测试）。
func NewJinaCrawlerDriver(doer httpx.Doer) *JinaCrawlerDriver {
	return &JinaCrawlerDriver{Doer: doer}
}

// Crawl 抓取。
func (d *JinaCrawlerDriver) Crawl(ctx context.Context, url, apiKey string) (string, error) {
	header := map[string]string{
		"User-Agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
		"Cache-Control":   "no-cache",
		"Pragma":          "no-cache",
		"X-Return-Format": "markdown",
		"X-No-Cache":      "true",
	}
	if apiKey != "" {
		header["Authorization"] = "Bearer " + apiKey
	}
	client := httpx.New(httpx.WithDoer(d.Doer))
	body, _, err := client.Get(ctx, "https://r.jina.ai/"+url, header)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// FirecrawlCrawlerDriver Firecrawl 驱动（POST /v1/scrape，markdown +
// onlyMainContent；成功取 data.markdown）。
type FirecrawlCrawlerDriver struct{ Doer httpx.Doer }

// NewFirecrawlCrawlerDriver 构造。
func NewFirecrawlCrawlerDriver(doer httpx.Doer) *FirecrawlCrawlerDriver {
	return &FirecrawlCrawlerDriver{Doer: doer}
}

// Crawl 抓取。
func (d *FirecrawlCrawlerDriver) Crawl(ctx context.Context, url, apiKey string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"url": url, "formats": []string{"markdown"}, "onlyMainContent": true,
	})
	header := map[string]string{"Content-Type": "application/json"}
	if apiKey != "" {
		header["Authorization"] = "Bearer " + apiKey
	}
	client := httpx.New(httpx.WithDoer(d.Doer))
	body, _, err := client.PostJSON(ctx, "https://api.firecrawl.dev/v1/scrape", payload, header)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Success bool   `json:"success"`
		Data    struct {
			Markdown string `json:"markdown"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("web: firecrawl response parse: %w", err)
	}
	if !parsed.Success {
		if parsed.Error != "" {
			return "", fmt.Errorf("web: firecrawl: %s", parsed.Error)
		}
		return "", fmt.Errorf("web: firecrawl: unsuccessful scrape")
	}
	return parsed.Data.Markdown, nil
}

// Crawler 爬虫能力包（对标 WebCrawlerDriverTalent：类名即名、无指令）。
type Crawler struct {
	ai.TalentBase
	driver CrawlerDriver
	apiKey string
}

// NewCrawler 构造。
func NewCrawler(driver CrawlerDriver) *Crawler { return &Crawler{driver: driver} }

// APIKey 密钥（JINA Bearer / Firecrawl 必填）。
func (t *Crawler) APIKey(k string) *Crawler { t.apiKey = k; return t }

// Driver 驱动替换。
func (t *Crawler) Driver(d CrawlerDriver) *Crawler { t.driver = d; return t }

// Name 能力包名（Java 类简名 WebCrawlerDriverTalent——Go 收敛去后缀）。
func (t *Crawler) Name() string { return "Crawler" }

// Description 描述。
func (t *Crawler) Description() string { return "网页爬取与 Markdown 转换" }

// Tools 工具集（crawl_url 单工具——与 Java 相同：无深度/同域开关/去重参数）。
func (t *Crawler) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{ai.NewTool("crawl_url").
		Desc("读取并分析指定 URL 网页的详细内容").
		StringParam("url", "目标网页地址", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			url := strings.TrimSpace(stringOf(args["url"]))
			if url == "" || (!strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://")) {
				return "错误：URL 无效。", nil
			}
			md, err := t.driver.Crawl(ctx, url, t.apiKey)
			if err != nil {
				return "抓取失败: " + err.Error(), nil
			}
			return md, nil
		}).
		Build()}
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}
