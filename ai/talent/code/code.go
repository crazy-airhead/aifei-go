// Package code 代码工程能力包（对标 CodeTalent + LanguageProvider：构建/测试
// 指令按技术栈检测生成 CODE.md 规约文件 + 指令注入；Go 首卡落 11 内置 Provider
// 中的核心六种（Maven/Gradle/Node/Go/Python/Rust），其余按 SPI 注入）。
package code

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// LanguageProvider 语言 Provider SPI（对标同名接口）。
type LanguageProvider interface {
	ID() string
	TypeName() string
	Markers() []string
	IgnoreFolders() []string
	IsMatch(dir string, entries map[string]bool) bool
	AppendRootCommands(sb *strings.Builder)
	AppendModuleCommands(sb *strings.Builder, module string)
	DetectVersion(dir string) string
}

// baseProvider 内置 Provider 基座。
type baseProvider struct {
	id, typeName  string
	markers       []string
	ignoreFolders []string
	rootCommands  []string
	versionRe     *regexp.Regexp
	versionPrefix string
}

func (p *baseProvider) ID() string        { return p.id }
func (p *baseProvider) TypeName() string  { return p.typeName }
func (p *baseProvider) Markers() []string { return p.markers }
func (p *baseProvider) IgnoreFolders() []string { return p.ignoreFolders }
func (p *baseProvider) IsMatch(dir string, entries map[string]bool) bool {
	for _, m := range p.markers {
		if entries[m] {
			return true
		}
	}
	return false
}
func (p *baseProvider) AppendRootCommands(sb *strings.Builder) {
	for _, c := range p.rootCommands {
		sb.WriteString(c + "\n")
	}
}
func (p *baseProvider) AppendModuleCommands(sb *strings.Builder, module string) {
	for _, c := range p.rootCommands {
		sb.WriteString("cd " + module + " && " + c + "\n")
	}
}
func (p *baseProvider) DetectVersion(dir string) string {
	if p.versionRe == nil {
		return ""
	}
	for _, m := range p.markers {
		data, err := os.ReadFile(filepath.Join(dir, m))
		if err != nil {
			continue
		}
		if match := p.versionRe.FindSubmatch(data); match != nil {
			return p.versionPrefix + " " + string(match[1])
		}
	}
	return ""
}

// BuiltinProviders 内置 Provider（Maven/Gradle/Node/Go/Python/Rust）。
func BuiltinProviders() []LanguageProvider {
	return []LanguageProvider{
		&baseProvider{id: "Go", typeName: "Go 模块", markers: []string{"go.mod"},
			ignoreFolders: []string{"vendor"},
			rootCommands: []string{"go mod tidy", "go build ./...", "go test ./...", "go test -v -run TestName ./..."},
			versionRe: regexp.MustCompile(`(?m)^\s*go\s+([\d.]+)\s*$`), versionPrefix: "Go"},
		&baseProvider{id: "Maven", typeName: "Maven 工程", markers: []string{"pom.xml"},
			ignoreFolders: []string{"target"},
			rootCommands: []string{"mvn clean compile", "mvn test -Dtest=ClassName"},
			versionRe: regexp.MustCompile(`<maven.compiler.source>([\d.]+)</maven.compiler.source>`), versionPrefix: "Java"},
		&baseProvider{id: "Gradle", typeName: "Gradle 工程", markers: []string{"build.gradle", "build.gradle.kts"},
			ignoreFolders: []string{"build"},
			rootCommands: []string{"./gradlew build", "./gradlew test --tests ClassName"}},
		&baseProvider{id: "Node", typeName: "Node 项目", markers: []string{"package.json"},
			ignoreFolders: []string{"node_modules"},
			rootCommands: []string{"npm install", "npm run build", "npm test"},
			versionRe: regexp.MustCompile(`"node":\s*"([\d.]+)"`), versionPrefix: "Node"},
		&baseProvider{id: "Python", typeName: "Python 项目", markers: []string{"requirements.txt", "pyproject.toml"},
			rootCommands: []string{"pip install -r requirements.txt", "python -m pytest -k TestName"}},
		&baseProvider{id: "Rust", typeName: "Rust 项目", markers: []string{"Cargo.toml"},
			ignoreFolders: []string{"target"},
			rootCommands: []string{"cargo build", "cargo test -- --nocapture"},
			versionRe: regexp.MustCompile(`(?m)^edition\s*=\s*"([\d.]+)"`), versionPrefix: "Rust edition"},
	}
}

// scanDirEntries 目录一级条目名集合。
func scanDirEntries(dir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

// CodeTalent 代码工程能力包。
type CodeTalent struct {
	ai.TalentBase
	workDir  string
	codeDir  string
	providers []LanguageProvider
	cache    map[string]*cachedDetection
}

type cachedDetection struct {
	supported bool
	at        time.Time
}

// New 构造（codeDir 缺省 .soloncode）。
func New(workDir string, providers ...LanguageProvider) *CodeTalent {
	if len(providers) == 0 {
		providers = BuiltinProviders()
	}
	return &CodeTalent{
		workDir: workDir, codeDir: filepath.Join(workDir, ".soloncode"),
		providers: providers, cache: map[string]*cachedDetection{},
	}
}

// Name 名称。
func (t *CodeTalent) Name() string { return "Code" }

// Description 描述。
func (t *CodeTalent) Description() string {
	return "代码专家。支持项目初始化、技术栈自动识别以及 CODE.md 规约生成。"
}

// CodeMdPath 规约文件路径。
func (t *CodeTalent) CodeMdPath() string { return filepath.Join(t.codeDir, "CODE.md") }

// IsSupported 准入（CODE.md 存在 / 根特征 / Provider 命中）。
func (t *CodeTalent) IsSupported(p *ai.Prompt) bool {
	if _, err := os.Stat(t.CodeMdPath()); err == nil {
		return true
	}
	entries := scanDirEntries(t.workDir)
	for _, hint := range []string{".git", ".github", ".gitee", "src", "lib"} {
		if entries[hint] {
			return true
		}
	}
	for _, provider := range t.providers {
		if provider.IsMatch(t.workDir, entries) {
			return true
		}
	}
	return false
}

// OnAttach 扫描并写 CODE.md。
func (t *CodeTalent) OnAttach(p *ai.Prompt) {
	_ = t.initCodeMd()
}

func (t *CodeTalent) initCodeMd() string {
	entries := scanDirEntries(t.workDir)
	root := providerMatch(t.providers, t.workDir, entries)

	var sb strings.Builder
	stacks := []string{}
	sb.WriteString("## 构建与测试指令\n")
	if root != nil {
		stacks = append(stacks, root.ID())
		root.AppendRootCommands(&sb)
	}

	// 子模块（深度 4，跳过隐藏与 ignore 目录）
	modules := map[string][]string{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 4 {
			return
		}
		subEntries := scanDirEntries(dir)
		for _, provider := range t.providers {
			if provider == root {
				continue
			}
			if provider.IsMatch(dir, subEntries) {
				rel, err := filepath.Rel(t.workDir, dir)
				if err != nil {
					continue
				}
				modules[provider.ID()] = append(modules[provider.ID()], filepath.ToSlash(rel))
			}
		}
		for name := range subEntries {
			if strings.HasPrefix(name, ".") {
				continue
			}
			if isIgnored(t.providers, name) {
				continue
			}
			sub := filepath.Join(dir, name)
			if info, err := os.Stat(sub); err == nil && info.IsDir() {
				walk(sub, depth+1)
			}
		}
	}
	walk(t.workDir, 1)

	if len(modules) > 0 {
		sb.WriteString("\n### 子模块与子项目\n")
		ids := make([]string, 0, len(modules))
		for id := range modules {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			dirs := modules[id]
			sort.Strings(dirs)
			provider := providerByID(t.providers, id)
			if provider == nil {
				continue
			}
			if root == nil || root.ID() != id {
				stacks = append(stacks, id)
				for _, d := range dirs {
					provider.AppendModuleCommands(&sb, d)
				}
			}
		}
	}

	sb.WriteString("\n## 环境版本\n")
	if root != nil {
		if v := root.DetectVersion(t.workDir); v != "" {
			sb.WriteString("- " + v + "\n")
		}
	}

	sb.WriteString(`
## 工程规约
1. 修改前必读 CODE.md（本文件）中的构建与测试指令
2. 每次修改保持原子性：一个提交只做一件事
3. 修改后必须运行对应测试验证
4. 仅使用相对路径引用工程内文件
5. 代码风格与既有代码对齐
6. 依赖版本与工程声明对齐
7. 环境感知：构建失败先检查环境版本
`)

	content := sb.String()
	if err := os.MkdirAll(t.codeDir, 0o755); err != nil {
		return "规约写入失败: " + err.Error()
	}
	if existing, err := os.ReadFile(t.CodeMdPath()); err == nil && string(existing) == content {
		return "已验证项目工程规范 (检测到技术栈: " + strings.Join(stacks, "/") + ")"
	}
	if err := os.WriteFile(t.CodeMdPath(), []byte(content), 0o644); err != nil {
		return "规约写入失败: " + err.Error()
	}
	t.ensureGitignore()
	if len(stacks) == 0 {
		return "已更新项目工程规范 (未检出明确技术栈)"
	}
	return "已更新项目工程规范 (检测到技术栈: " + strings.Join(stacks, "/") + ")"
}

// ensureGitignore 已有 .gitignore 时补 codeDir。
func (t *CodeTalent) ensureGitignore() {
	gitignore := filepath.Join(t.workDir, ".gitignore")
	data, err := os.ReadFile(gitignore)
	if err != nil {
		return
	}
	target := ".soloncode"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(strings.TrimSuffix(line, "/")) == target {
			return
		}
	}
	f, err := os.OpenFile(gitignore, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if !strings.HasSuffix(string(data), "\n") {
		f.WriteString("\n")
	}
	f.WriteString(target + "\n")
}

// Instruction 指令注入。
func (t *CodeTalent) Instruction(p *ai.Prompt) string {
	return `## 核心工程规约 (Core Engineering Protocol)
> 项目当前上下文: ` + t.initCodeMd() + `
- 动作前先读 CODE.md（` + t.CodeMdPath() + `）
- 修改后按其中指令运行测试`
}

// Tools 无工具（纯指令注入型——Java 同款）。
func (t *CodeTalent) Tools(*ai.Prompt) []ai.FunctionTool { return nil }

func providerMatch(providers []LanguageProvider, dir string, entries map[string]bool) LanguageProvider {
	for _, p := range providers {
		if p.IsMatch(dir, entries) {
			return p
		}
	}
	return nil
}

func providerByID(providers []LanguageProvider, id string) LanguageProvider {
	for _, p := range providers {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

func isIgnored(providers []LanguageProvider, name string) bool {
	for _, p := range providers {
		for _, f := range p.IgnoreFolders() {
			if f == name {
				return true
			}
		}
	}
	return false
}
