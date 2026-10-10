// command.go 斜杠命令体系（对标 harness.command 包）：Command 接口 +
// CommandRegistry（重名告警跳过）+ MarkdownCommand（Claude Code 自定义命令
// 兼容：frontmatter + $ARGUMENTS/$1..$N 模板替换）+ 目录加载（子目录映射为
// deploy:staging 冒号名）。
package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/crazy-airhead/aifei-go/log"
)

// Command 斜杠命令接口（name 不含 /）。
type Command interface {
	Name() string
	Description() string
	Examples() []string
	Model() string         // 指定执行模型（空 = 缺省）
	CliOnly() bool         // Web 端不展示不执行
	RunnableWhenBusy() bool
	Execute(ctx CommandContext) error
}

// CommandBase 默认实现（嵌入按需覆写）。
type CommandBase struct{}

func (CommandBase) Description() string    { return "" }
func (CommandBase) Examples() []string     { return nil }
func (CommandBase) Model() string          { return "" }
func (CommandBase) CliOnly() bool          { return false }
func (CommandBase) RunnableWhenBusy() bool { return false }
func (CommandBase) Execute(CommandContext) error { return nil }

// CommandContext 命令执行上下文（端无关：CLI/Web 共用）。
type CommandContext interface {
	RawInput() string
	CommandName() string
	Args() []string
	// RunAgentTask 以解析后的提示跑主代理（异步回调；CLI 阻塞等待、Web 返回流）。
	RunAgentTask(prompt, model string)
}

// CommandRegistry 命令表（putIfAbsent 防覆盖，重名告警跳过——Java 同款）。
type CommandRegistry struct {
	commands map[string]Command
}

// NewCommandRegistry 构造。
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{commands: map[string]Command{}}
}

// Register 注册（重名告警跳过）。
func (r *CommandRegistry) Register(c Command) {
	if c == nil || c.Name() == "" {
		return
	}
	if _, exists := r.commands[c.Name()]; exists {
		log.Warn("harness: command '%s' already registered, skip", c.Name())
		return
	}
	r.commands[c.Name()] = c
}

// Find 查找。
func (r *CommandRegistry) Find(name string) Command { return r.commands[name] }

// All 全部（按名排序）。
func (r *CommandRegistry) All() []Command {
	out := make([]Command, 0, len(r.commands))
	for _, c := range r.commands {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Names 名称集（Tab 补全）。
func (r *CommandRegistry) Names() []string {
	out := make([]string, 0, len(r.commands))
	for name := range r.commands {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// LoadDir 目录加载（递归扫描 .md；deploy/staging.md → deploy:staging）。
func (r *CommandRegistry) LoadDir(baseDir string) error {
	return filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 缺目录/权限等静默跳过（Java walk 同款容忍）
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(rel, ".md")
		name = strings.ReplaceAll(filepath.ToSlash(name), "/", ":")
		cmd, err := ParseMarkdownCommand(name, string(data))
		if err != nil {
			return err
		}
		r.Register(cmd)
		return nil
	})
}

// markdownCommandMeta frontmatter 字段（Claude Code 自定义命令形态）。
type markdownCommandMeta struct {
	Description   string   `yaml:"description"`
	ArgumentHint  any      `yaml:"argument-hint"`
	AllowedTools  string   `yaml:"allowed-tools"`
	Model         string   `yaml:"model"`
}

// MarkdownCommand markdown 自定义命令（模板 + $ARGUMENTS/$1..$N 替换）。
type MarkdownCommand struct {
	CommandBase
	name        string
	DescriptionText string
	ModelName  string
	Template    string
}

// ParseMarkdownCommand 解析（frontmatter + 正文模板）。
func ParseMarkdownCommand(name, content string) (*MarkdownCommand, error) {
	meta := markdownCommandMeta{}
	body := content
	if strings.HasPrefix(content, "---\n") {
		rest := content[4:]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			if err := yaml.Unmarshal([]byte(rest[:end]), &meta); err != nil {
				return nil, fmt.Errorf("harness: command frontmatter parse: %w", err)
			}
			body = strings.TrimLeft(rest[end+4:], "\r\n")
		}
	}
	desc := meta.Description
	if desc == "" {
		desc = "Custom command: " + name
	}
	return &MarkdownCommand{name: name, DescriptionText: desc, ModelName: meta.Model, Template: body}, nil
}

// Name 名称。
func (c *MarkdownCommand) Name() string { return c.name }

// Description 描述。
func (c *MarkdownCommand) Description() string { return c.DescriptionText }

// Model 模型。
func (c *MarkdownCommand) Model() string { return c.ModelName }

// ResolvedPrompt 模板解析（先 $1..$N 后 $ARGUMENTS）。
func (c *MarkdownCommand) ResolvedPrompt(args []string) string {
	out := c.Template
	for i, a := range args {
		out = strings.ReplaceAll(out, fmt.Sprintf("$%d", i+1), a)
	}
	out = strings.ReplaceAll(out, "$ARGUMENTS", strings.Join(args, " "))
	return out
}

// Execute 执行（runAgentTask 交给主代理）。
func (c *MarkdownCommand) Execute(ctx CommandContext) error {
	ctx.RunAgentTask(c.ResolvedPrompt(ctx.Args()), c.ModelName)
	return nil
}
