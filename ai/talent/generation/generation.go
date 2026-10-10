// Package generation 图像/视频生成能力包（对标 ImageGenerationTalent /
// VideoGenerationTalent：ImageDriver/VideoDriver 驱动 SPI + 文件名消毒/去重/
// 路径沙箱——Go 侧以 ai/generate.Model 适配注入）。
package generation

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	igen "github.com/crazy-airhead/aifei-go/ai/generate"
)

// ImageDriver 图像驱动（对标同名接口；Go 侧 generate.Model 的适配器见
// NewModelImageDriver）。
type ImageDriver interface {
	Generate(ctx context.Context, prompt, size, savePath, apiKey string) (string, error)
}

// VideoDriver 视频驱动。
type VideoDriver interface {
	Generate(ctx context.Context, prompt string, seconds int, ratio, savePath, apiKey string) (string, error)
}

// ModelImageDriver generate.Model → ImageDriver 适配。
type ModelImageDriver struct {
	Model   *igen.Model
	APIKey  string
}

// NewModelImageDriver 构造。
func NewModelImageDriver(m *igen.Model) *ModelImageDriver { return &ModelImageDriver{Model: m} }

// Generate 生成并落盘（size 透传、结果 Content url/data 下载到 savePath）。
func (d *ModelImageDriver) Generate(ctx context.Context, prompt, size, savePath, _ string) (string, error) {
	if d.Model == nil {
		return "", fmt.Errorf("generation: model is required")
	}
	resp, err := d.Model.Prompt(prompt).Size(size).Call(ctx)
	if err != nil {
		return "", err
	}
	if !resp.HasData() {
		return "", fmt.Errorf("generation: no data returned")
	}
	content := resp.Content()
	value := content.Value()
	if value == "" {
		return "", fmt.Errorf("generation: empty content")
	}
	if strings.HasPrefix(value, "http") {
		if err := downloadTo(ctx, value, savePath); err != nil {
			return "", err
		}
		return "Image generated", nil
	}
	// base64 data
	if err := os.WriteFile(savePath, []byte(value), 0o644); err != nil {
		return "", err
	}
	return "Image generated", nil
}

// sanitizeFileName 文件名消毒（空 → img_{8}; 非法字符 → _; 补扩展名）。
var fileNameSanitizer = regexp.MustCompile(`[\\/:*?"<>|]`)

func sanitizeFileName(name, defPrefix, ext string) string {
	if strings.TrimSpace(name) == "" {
		return defPrefix + shortID() + ext
	}
	name = fileNameSanitizer.ReplaceAllString(name, "_")
	if !strings.Contains(filepath.Ext(name), ".") || len(filepath.Ext(name)) > 5 {
		name += ext
	}
	return name
}

var hexDigits = "0123456789abcdef"

func shortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	out := make([]byte, 8)
	for i, v := range b {
		out[i*2] = hexDigits[v>>4]
		out[i*2+1] = hexDigits[v&0x0f]
	}
	return string(out)
}

// downloadTo URL → 落盘（10s/60s 超时——Java downloadImage 同款）。
func downloadTo(ctx context.Context, url, savePath string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "aifei-go-agent/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("generation: download status %d", resp.StatusCode)
	}
	f, err := os.Create(savePath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// uniquePath 重名自动 _1/_2 递增。
func uniquePath(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
}

// resolveSandboxed 路径沙箱（rootPath 内解析 + 越界拒绝）。
func resolveSandboxed(rootPath, name string) (string, error) {
	if rootPath == "" {
		rootPath = "."
	}
	abs, err := filepath.Abs(filepath.Join(rootPath, name))
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) && abs != root {
		return "", fmt.Errorf("generation: 非法路径访问")
	}
	return abs, nil
}

// Image 能力包。
type Image struct {
	ai.TalentBase
	driver ImageDriver
	apiKey string
	workDir string
}

// NewImage 构造。
func NewImage(driver ImageDriver, workDir string) *Image { return &Image{driver: driver, workDir: workDir} }

// APIKey 密钥。
func (t *Image) APIKey(k string) *Image { t.apiKey = k; return t }

// Name 名称。
func (t *Image) Name() string { return "ImageGeneration" }

// Description 描述。
func (t *Image) Description() string { return "图像生成专家" }

// IsSupported 关键词准入。
func (t *Image) IsSupported(p *ai.Prompt) bool { return promptHas(p, "画", "生成图片", "image", "draw", "pic") }

// Tools 工具集。
func (t *Image) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{ai.NewTool("generate_image").
		Desc("根据提示生成图像。尺寸建议：头像 512x512、标准 1024x1024、海报 1024x1792。").
		StringParam("prompt", "图像内容描述", true).
		StringParam("size", "尺寸（宽x高，默认 1024x1024）", false).
		StringParam("fileName", "保存文件名（可选，默认自动）", false).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			prompt, _ := args["prompt"].(string)
			size, _ := args["size"].(string)
			if size == "" {
				size = "1024x1024"
			}
			fileName, _ := args["fileName"].(string)
			fileName = sanitizeFileName(fileName, "img_", ".png")
			path, err := resolveSandboxed(t.workDir, fileName)
			if err != nil {
				return err.Error(), nil
			}
			path = uniquePath(path)
			result, err := t.driver.Generate(ctx, prompt, size, path, t.apiKey)
			if err != nil {
				return "生成失败: " + err.Error(), nil
			}
			return result + " [Saved as: " + filepath.Base(path) + "]", nil
		}).
		Build()}
}

// Video 能力包。
type Video struct {
	ai.TalentBase
	driver VideoDriver
	apiKey string
	workDir string
}

// NewVideo 构造。
func NewVideo(driver VideoDriver, workDir string) *Video { return &Video{driver: driver, workDir: workDir} }

// APIKey 密钥。
func (t *Video) APIKey(k string) *Video { t.apiKey = k; return t }

// Name 名称。
func (t *Video) Name() string { return "VideoGeneration" }

// Description 描述。
func (t *Video) Description() string { return "视频生成专家" }

// IsSupported 关键词准入。
func (t *Video) IsSupported(p *ai.Prompt) bool { return promptHas(p, "视频", "video", "短片", "制作", "mp4") }

// Tools 工具集。
func (t *Video) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{ai.NewTool("generate_video").
		Desc("根据提示生成短视频").
		StringParam("prompt", "视频内容描述", true).
		IntParam("seconds", "时长秒数（默认 5）", false).
		StringParam("aspectRatio", "宽高比（默认 16:9）", false).
		StringParam("fileName", "保存文件名（可选，默认自动）", false).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			prompt, _ := args["prompt"].(string)
			seconds, _ := args["seconds"].(float64)
			if seconds <= 0 {
				seconds = 5
			}
			ratio, _ := args["aspectRatio"].(string)
			if ratio == "" {
				ratio = "16:9"
			}
			fileName, _ := args["fileName"].(string)
			fileName = sanitizeFileName(fileName, "v_", ".mp4")
			path, err := resolveSandboxed(t.workDir, fileName)
			if err != nil {
				return err.Error(), nil
			}
			path = uniquePath(path)
			result, err := t.driver.Generate(ctx, prompt, int(seconds), ratio, path, t.apiKey)
			if err != nil {
				return "生成失败: " + err.Error(), nil
			}
			return result + fmt.Sprintf(" [File: %s, Duration: %ds, Ratio: %s]", filepath.Base(path), int(seconds), ratio), nil
		}).
		Build()}
}

// promptHas 提示词关键词准入（用户消息文本）。
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
