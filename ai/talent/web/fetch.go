// Package web 网络能力包基础面（对标 solon-ai-talent-web，06 契约收窄）：
// Fetch=WebfetchTalent 移植（超时钳制/UA 与按格式 Accept 头/重试/Cloudflare
// 403+challenge 换 UA 重试/5MB 上限/图片 base64 data URI/三格式转换）；HTML
// 转 Markdown 为零依赖尽力转换（Java 侧 flexmark 级保真转 P5 后期，契约
// 明示）；Search 替代 Java 的 Exa MCP 调用——委托 ai/rag/search 仓储（三
// 家搜索即仓储任一皆可注入）。charset 处理收窄：仅按 UTF-8 宽容解码（Go
// 标准库无转换表，非 UTF-8 页面出替换符，偏离注明）。
package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

const (
	fetchDefaultTimeoutMS = 30_000
	fetchMaxTimeoutMS     = 120_000
	fetchMaxResponseSize  = 5 * 1024 * 1024 // 5MB 硬限制
	fetchDefaultUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"
)

// Fetch 网页抓取能力包（webfetch 工具，纯工具集形态）。
type Fetch struct {
	ai.TalentBase
	doer       httpx.Doer
	maxRetries int
	userAgent  string
}

// NewFetch 构造（缺省重试 3 次、Chrome UA）。
func NewFetch() *Fetch {
	return &Fetch{doer: &http.Client{}, maxRetries: 3, userAgent: fetchDefaultUA}
}

// RetryConfig 覆写最大重试次数（>=1）。
func (t *Fetch) RetryConfig(n int) *Fetch {
	if n >= 1 {
		t.maxRetries = n
	}
	return t
}

// UserAgent 覆写 UA（空串忽略）。
func (t *Fetch) UserAgent(ua string) *Fetch {
	if ua != "" {
		t.userAgent = ua
	}
	return t
}

// Doer 注入 HTTP 执行缝（测试桩/仪表化客户端）。
func (t *Fetch) Doer(d httpx.Doer) *Fetch {
	if d != nil {
		t.doer = d
	}
	return t
}

func (t *Fetch) Name() string { return "webfetch" }

func (t *Fetch) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("webfetch").
			Desc("从 URL 获取网页内容。返回 Markdown，正文前包含 YAML Front Matter 元数据。").
			StringParam("url", "目标网页的完整 URL（必须包含 http:// 或 https://）", true).
			Param(ai.ParamSpec{Name: "format", Type: "string", Required: false, Default: "markdown",
				Desc: "返回格式：'markdown', 'text', 'html'"}).
			Param(ai.ParamSpec{Name: "timeoutMs", Type: "integer", Required: false,
				Desc: "超时时间（毫秒），最大 120_000 毫秒"}).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				format, _ := args["format"].(string)
				return t.Fetch(ctx, argStr(args, "url"), format, argInt(args, "timeoutMs"))
			}).Build(),
	}
}

// Fetch 抓取主流程（命名避让 Talent.Fetch；逻辑逐段对标 Java webfetch）。
func (t *Fetch) Fetch(ctx context.Context, rawURL, format string, timeoutMS int) (string, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return "", errors.New("URL must start with http:// or https://")
	}
	if timeoutMS <= 0 {
		timeoutMS = fetchDefaultTimeoutMS
	}
	if timeoutMS > fetchMaxTimeoutMS {
		timeoutMS = fetchMaxTimeoutMS
	}
	finalFormat := strings.ToLower(format)
	if finalFormat == "" {
		finalFormat = "markdown"
	}
	cctx, cancel := context.WithTimeout(ctx, msDuration(timeoutMS))
	defer cancel()

	header := map[string]string{
		"User-Agent":      t.userAgent,
		"Accept":          acceptHeader(finalFormat),
		"Accept-Language": "en-US,en;q=0.9",
	}
	var resp *http.Response
	var err error
	for attempt := 0; ; attempt++ {
		resp, err = t.exec(cctx, rawURL, header)
		if err == nil {
			break
		}
		if attempt >= t.maxRetries-1 {
			return "", err
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Request failed with status code: %d", resp.StatusCode)
	}
	if resp.ContentLength > fetchMaxResponseSize {
		return "", errors.New("Response too large (exceeds 5MB limit)")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxResponseSize+1))
	if err != nil {
		return "", err
	}
	if int64(len(body)) > fetchMaxResponseSize {
		return "", errors.New("Response too large (exceeds 5MB limit)")
	}

	contentType := resp.Header.Get("Content-Type")
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))

	// 图片直转 data URI（svg 与 vnd.fastbidsheet 除外——Java 同款判定）
	if strings.HasPrefix(mime, "image/") && !strings.Contains(mime, "svg") &&
		!strings.Contains(mime, "vnd.fastbidsheet") {
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body), nil
	}

	raw := string(body) // charset 宽容：非 UTF-8 声明亦按 UTF-8 解码（替换符兜底）
	isHTML := mime == "text/html"
	switch {
	case finalFormat == "markdown" && isHTML:
		return HTMLToMarkdown(raw), nil
	case finalFormat == "text" && isHTML:
		return ExtractTextFromHTML(raw), nil
	default:
		return raw, nil
	}
}

// exec 单次请求；Cloudflare 盾（403 + cf-mitigated: challenge）换 UA
// "opencode" 立即重试一次（Java 穿透逻辑同款）。
func (t *Fetch) exec(ctx context.Context, rawURL string, header map[string]string) (*http.Response, error) {
	resp, err := t.do(ctx, rawURL, header)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("cf-mitigated") == "challenge" {
		resp.Body.Close()
		retry := make(map[string]string, len(header)+1)
		for k, v := range header {
			retry[k] = v
		}
		retry["User-Agent"] = "opencode"
		return t.do(ctx, rawURL, retry)
	}
	return resp, nil
}

func (t *Fetch) do(ctx context.Context, rawURL string, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return t.doer.Do(req)
}

// acceptHeader 按格式构造 Accept（Java getAcceptHeader 逐字移植）。
func acceptHeader(format string) string {
	switch format {
	case "markdown":
		return "text/markdown;q=1.0, text/x-markdown;q=0.9, text/plain;q=0.8, text/html;q=0.7, */*;q=0.1"
	case "text":
		return "text/plain;q=1.0, text/markdown;q=0.9, text/html;q=0.8, */*;q=0.1"
	case "html":
		return "text/html;q=1.0, application/xhtml+xml;q=0.9, text/plain;q=0.8, text/markdown;q=0.7, */*;q=0.1"
	default:
		return "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	}
}

// ---- 参数取值 ----

func argStr(args map[string]any, key string) string {
	if s, ok := args[key].(string); ok {
		return s
	}
	return ""
}

func argInt(args map[string]any, key string) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	if v, ok := args[key].(int); ok {
		return v
	}
	return 0
}

// msDuration 毫秒 → Duration。
func msDuration(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
