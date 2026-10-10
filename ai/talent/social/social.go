// Package social IM webhook 能力包（对标钉钉/飞书/企微三 Talent：HMAC-SHA256
// 签名两形态——钉钉签名在 URL、飞书签名在 body（数据同为 ts+"\n"+secret）；
// 企微无签名；markdown/text 消息形态切换）。
package social

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// postJSON 通用 POST（10/30/30 超时——Java 同款）。
func postJSON(ctx context.Context, rawURL, payload string) (string, error) {
	client := httpx.New()
	body, _, err := client.PostJSON(ctx, rawURL, []byte(payload), map[string]string{
		"Content-Type": "application/json;charset=utf-8",
	})
	return string(body), err
}

// signHmac HmacSHA256(ts+"\n"+secret, secret) → base64（钉钉/飞书共用数据形态）。
func signHmac(ts int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ---- 钉钉 ----

// DingTalk 钉钉能力包。
type DingTalk struct {
	ai.TalentBase
	webhookURL string
	secret     string
}

// NewDingTalk 构造。
func NewDingTalk(webhookURL, secret string) *DingTalk {
	return &DingTalk{webhookURL: webhookURL, secret: secret}
}

// Name 名称。
func (t *DingTalk) Name() string { return "DingTalk" }

// Description 描述。
func (t *DingTalk) Description() string { return "钉钉消息推送" }

// IsSupported 关键词准入。
func (t *DingTalk) IsSupported(p *ai.Prompt) bool { return promptHas(p, "钉钉", "ding") }

// Tools 工具集。
func (t *DingTalk) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{ai.NewTool("send_dingtalk").
		Desc("发送钉钉消息（markdown 或纯文本，可 @ 手机号）").
		StringParam("title", "标题（有则 markdown 形态）", false).
		StringParam("text", "正文", true).
		StringParam("atMobiles", "被 @ 的手机号（逗号分隔，可选）", false).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			title, _ := args["title"].(string)
			text, _ := args["text"].(string)
			var payload string
			if title != "" {
				payload = fmt.Sprintf(`{"msgtype":"markdown","markdown":{"title":%q,"text":%q}}`, title, text)
			} else {
				payload = fmt.Sprintf(`{"msgtype":"text","text":{"content":%q}}`, text)
			}
			if atMobiles, _ := args["atMobiles"].(string); atMobiles != "" {
				var arr []string
				for _, m := range strings.Split(atMobiles, ",") {
					if m = strings.TrimSpace(m); m != "" {
						arr = append(arr, `"`+m+`"`)
					}
				}
				if payload == fmt.Sprintf(`{"msgtype":"text","text":{"content":%q}}`, text) {
					payload = strings.TrimSuffix(payload, "}") + `,"at":{"atMobiles":[` + strings.Join(arr, ",") + `]}}`
				}
			}
			// 签名在 URL
			target := t.webhookURL
			if t.secret != "" {
				ts := time.Now().UnixMilli()
				sign := url.QueryEscape(signHmac(ts, t.secret))
				target = fmt.Sprintf("%s&timestamp=%d&sign=%s", target, ts, sign)
			}
			res, err := postJSON(ctx, target, payload)
			if err != nil {
				return "发送异常: " + err.Error(), nil
			}
			if strings.Contains(res, `"errcode":0`) {
				return "发送成功", nil
			}
			return "发送失败: " + res, nil
		}).
		Build()}
}

// SignedURL 签名 URL（测试面）。
func (t *DingTalk) SignedURL(ts int64) string {
	return fmt.Sprintf("%s&timestamp=%d&sign=%s", t.webhookURL, ts, url.QueryEscape(signHmac(ts, t.secret)))
}

// ---- 飞书 ----

// Feishu 飞书能力包。
type Feishu struct {
	ai.TalentBase
	webhookURL string
	secret     string
}

// NewFeishu 构造。
func NewFeishu(webhookURL, secret string) *Feishu {
	return &Feishu{webhookURL: webhookURL, secret: secret}
}

// Name 名称。
func (t *Feishu) Name() string { return "Lark" }

// Description 描述。
func (t *Feishu) Description() string { return "飞书消息推送" }

// IsSupported 关键词准入。
func (t *Feishu) IsSupported(p *ai.Prompt) bool { return promptHas(p, "飞书", "lark", "feishu") }

// Tools 工具集。
func (t *Feishu) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{ai.NewTool("send_lark").
		Desc("发送飞书消息（卡片或纯文本）").
		StringParam("title", "卡片标题（有则 interactive 卡片）", false).
		StringParam("text", "正文", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			title, _ := args["title"].(string)
			text, _ := args["text"].(string)
			ts := time.Now().Unix()
			var payload string
			if title != "" {
				payload = fmt.Sprintf(`{"timestamp":"%d","sign":%q,"msg_type":"interactive",`+
					`"card":{"header":{"title":{"tag":"plain_text","content":%q},"template":"blue"},`+
					`"elements":[{"tag":"div","text":{"tag":"lark_md","content":%q}}]}}`,
					ts, signHmac(ts, t.secret), title, text)
			} else {
				payload = fmt.Sprintf(`{"timestamp":"%d","sign":%q,"msg_type":"text","content":{"text":%q}}`,
					ts, signHmac(ts, t.secret), text)
			}
			res, err := postJSON(ctx, t.webhookURL, payload)
			if err != nil {
				return "发送异常: " + err.Error(), nil
			}
			if strings.Contains(res, `"code":0`) || strings.Contains(res, `"StatusCode":0`) {
				return "发送成功", nil
			}
			return "发送失败: " + res, nil
		}).
		Build()}
}

// ---- 企微 ----

// WeCom 企业微信能力包。
type WeCom struct {
	ai.TalentBase
	webhookURL string
}

// NewWeCom 构造。
func NewWeCom(webhookURL string) *WeCom { return &WeCom{webhookURL: webhookURL} }

// Name 名称。
func (t *WeCom) Name() string { return "WeCom" }

// Description 描述。
func (t *WeCom) Description() string { return "企业微信消息推送" }

// IsSupported 关键词准入。
func (t *WeCom) IsSupported(p *ai.Prompt) bool { return promptHas(p, "企微", "企业微信", "wecom") }

// Tools 工具集。
func (t *WeCom) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{ai.NewTool("send_wecom").
		Desc("发送企业微信消息（markdown 或纯文本）").
		StringParam("content", "内容（含 #/**/> 走 markdown）", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			content, _ := args["content"].(string)
			var payload string
			if strings.Contains(content, "#") || strings.Contains(content, "**") || strings.Contains(content, ">") {
				payload = fmt.Sprintf(`{"msgtype":"markdown","markdown":{"content":%q}}`, content)
			} else {
				payload = fmt.Sprintf(`{"msgtype":"text","text":{"content":%q}}`, content)
			}
			res, err := postJSON(ctx, t.webhookURL, payload)
			if err != nil {
				return "发送异常: " + err.Error(), nil
			}
			if strings.Contains(res, `"errcode":0`) {
				return "发送成功", nil
			}
			return "发送失败: " + res, nil
		}).
		Build()}
}

// promptHas 关键词准入（导出共用——social 包内三家复用，亦可被测试断言）。
func promptHas(p *ai.Prompt, keywords ...string) bool {
	if p == nil {
		return false
	}
	for _, m := range p.Messages() {
		if m.Role != ai.RoleUser {
			continue
		}
		if s, ok := m.Content.(string); ok {
			lower := strings.ToLower(s)
			for _, kw := range keywords {
				if strings.Contains(lower, strings.ToLower(kw)) {
					return true
				}
			}
		}
	}
	return false
}

// PromptHas 关键词准入导出面。
func PromptHas(p *ai.Prompt, keywords ...string) bool { return promptHas(p, keywords...) }

// SignForTest 签名计算的导出面（黄金断言用）。
func SignForTest(ts int64, secret string) string { return signHmac(ts, secret) }
