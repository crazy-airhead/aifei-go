// talents2_test.go 零依赖批七件黑盒测试：generation（驱动注入/文件名消毒/
// 去重/准入）、social（三 webhook 形态/双签名/HMAC 黄金）、mail（HTML 探测/
// 附件沙箱/httptest 无关——SMTP 不可测改 Mailer 注入）、mount（别名解析/
// 技能代理目录扫描/front matter）、memory（分词/检索两段评分/TTL/consolidate
// 读回校验/覆盖序）、code（Provider 检测/CODE.md 生成/gitignore）、data
//（KV 注入/最终键/文案）。
package ai_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	code "github.com/crazy-airhead/aifei-go/ai/talent/code"
	data "github.com/crazy-airhead/aifei-go/ai/talent/data"
	generation "github.com/crazy-airhead/aifei-go/ai/talent/generation"
	mail "github.com/crazy-airhead/aifei-go/ai/talent/mail"
	memory "github.com/crazy-airhead/aifei-go/ai/talent/memory"
	mount "github.com/crazy-airhead/aifei-go/ai/talent/mount"
	social "github.com/crazy-airhead/aifei-go/ai/talent/social"
)

func callToolStr(t *testing.T, tool ai.FunctionTool, args map[string]any) string {
	t.Helper()
	r := tool.Call(context.Background(), args)
	if r == nil {
		t.Fatal("空结果")
	}
	return r.ContentText()
}

// ---- generation ----

type stubImageDriver struct{ calls []string }

func (d *stubImageDriver) Generate(ctx context.Context, prompt, size, savePath, apiKey string) (string, error) {
	d.calls = append(d.calls, prompt+"|"+size+"|"+savePath)
	return "生成完成", nil
}

func TestTalentGenerationImage(t *testing.T) {
	dir := t.TempDir()
	driver := &stubImageDriver{}
	g := generation.NewImage(driver, dir).APIKey("k")
	if !g.IsSupported(ai.PromptOf("帮我画一只猫")) {
		t.Fatal("画 关键词准入")
	}
	if g.IsSupported(ai.PromptOf("写周报")) {
		t.Fatal("无关键词不应准入")
	}

	tools := g.Tools(nil)
	out := callToolStr(t, tools[0], map[string]any{"prompt": "一只猫", "size": "512x512"})
	if !strings.Contains(out, "[Saved as: ") || !strings.Contains(out, "生成完成") {
		t.Fatalf("输出：%q", out)
	}
	// 文件名消毒 + 自动扩展
	out = callToolStr(t, tools[0], map[string]any{"prompt": "x", "fileName": `非法/名:字*?`})
	saved := driver.calls[len(driver.calls)-1]
	if strings.ContainsAny(filepath.Base(saved), `/:*?"<>|`) {
		t.Fatalf("消毒：%s", saved)
	}
	if !strings.HasSuffix(saved, ".png") {
		t.Fatalf("自动补扩展名：%s", saved)
	}
	// 空名 → img_ 前缀
	out = callToolStr(t, tools[0], map[string]any{"prompt": "x"})
	if !strings.Contains(filepath.Base(driver.calls[len(driver.calls)-1]), "img_") {
		t.Fatalf("缺省名：%s", driver.calls[len(driver.calls)-1])
	}
	_ = out
}

func TestTalentGenerationVideo(t *testing.T) {
	dir := t.TempDir()
	driver := &stubVideoDriver{}
	v := generation.NewVideo(driver, dir)
	if !v.IsSupported(ai.PromptOf("制作一个视频短片 mp4")) {
		t.Fatal("视频关键词准入")
	}
	out := callToolStr(t, v.Tools(nil)[0], map[string]any{"prompt": "日落"})
	if !strings.Contains(out, "[File: ") || !strings.Contains(out, "Ratio: 16:9") {
		t.Fatalf("输出：%q", out)
	}
}

type stubVideoDriver struct{}

func (d *stubVideoDriver) Generate(ctx context.Context, prompt string, seconds int, ratio, savePath, apiKey string) (string, error) {
	return "视频就绪", nil
}

// ---- social ----

func TestTalentSocialHmacSign(t *testing.T) {
	// 黄金样本：ts+"\n"+secret 以 secret 为 key
	ts := int64(1700000000000)
	secret := "SEC123"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join([]string{"1700000000000", secret}, "\n")))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got := social.SignForTest(ts, secret); got != want {
		t.Fatalf("HMAC：%s want %s", got, want)
	}
}

func TestTalentSocialDingTalk(t *testing.T) {
	// httptest webhook 回放
	var body, urlPath string
	srv := newWebhookServer(t, &body, &urlPath, `{"errcode":0}`)
	defer srv.Close()

	dt := social.NewDingTalk(srv.URL+"/hook?token=x", "SEC")
	if !dt.IsSupported(ai.PromptOf("发个钉钉通知")) {
		t.Fatal("钉钉准入")
	}
	out := callToolStr(t, dt.Tools(nil)[0], map[string]any{"title": "T", "text": "内容"})
	if out != "发送成功" {
		t.Fatalf("输出：%q（url=%s body=%s）", out, urlPath, body)
	}
	// 签名在 URL（timestamp & sign 参数）
	if !strings.Contains(urlPath, "timestamp=") || !strings.Contains(urlPath, "sign=") {
		t.Fatalf("URL 签名：%s", urlPath)
	}
	// markdown 形态
	if !strings.Contains(body, `"msgtype":"markdown"`) || !strings.Contains(body, `"title":"T"`) {
		t.Fatalf("markdown body：%s", body)
	}
	// 无 title → text
	out = callToolStr(t, dt.Tools(nil)[0], map[string]any{"text": "纯文本"})
	if !strings.Contains(body, `"msgtype":"text"`) {
		t.Fatalf("text body：%s", body)
	}
	_ = out
	// 失败文案
	fail := social.NewDingTalk(srv.URL+"/hook?token=x", "")
	_ = fail
}

func TestTalentSocialFeishuWeCom(t *testing.T) {
	var body, _p string
	srv := newWebhookServer(t, &body, &_p, `{"errcode":0,"code":0}`)
	defer srv.Close()

	f := social.NewFeishu(srv.URL+"/hook", "SEC")
	if !f.IsSupported(ai.PromptOf("发飞书消息")) {
		t.Fatal("飞书准入")
	}
	if out := callToolStr(t, f.Tools(nil)[0], map[string]any{"title": "T", "text": "X"}); out != "发送成功" {
		t.Fatalf("飞书输出：%q body=%s", out, body)
	}
	// 签名在 body
	if !strings.Contains(body, `"sign":"`) || !strings.Contains(body, `"timestamp":"`) {
		t.Fatalf("body 签名：%s", body)
	}
	// 卡片形态
	if !strings.Contains(body, `"msg_type":"interactive"`) {
		t.Fatalf("卡片 body：%s", body)
	}

	w := social.NewWeCom(srv.URL+"/hook")
	if !w.IsSupported(ai.PromptOf("发企微消息")) {
		t.Fatal("企微准入")
	}
	if out := callToolStr(t, w.Tools(nil)[0], map[string]any{"content": "# 标题"}); out != "发送成功" {
		t.Fatalf("企微输出：%q", out)
	}
	if !strings.Contains(body, `"msgtype":"markdown"`) {
		t.Fatalf("markdown 形态：%s", body)
	}
}

// ---- mail ----

type stubMailer struct{ last lastMail }

type lastMail struct {
	to, subject, body string
	html              bool
	attachments       map[string][]byte
}

func (m *stubMailer) SendMail(ctx context.Context, from, to, subject, body string, attachments map[string][]byte, html bool) error {
	m.last = lastMail{to, subject, body, html, attachments}
	return nil
}

func TestTalentMail(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("附件内容"), 0o644)
	m := mail.NewMail(&stubMailer{}, dir)

	tools := m.Tools(nil)
	out := callToolStr(t, tools[0], map[string]any{
		"to": "x@y.z", "subject": "S", "body": "<b>HTML</b> 正文", "attachmentFileName": "a.txt",
	})
	if out != "邮件发送成功: x@y.z" {
		t.Fatalf("输出：%q", out)
	}
	sm := m.MailerForTest().(*stubMailer)
	if !sm.last.html {
		t.Fatal("HTML 探测")
	}
	if sm.last.attachments["a.txt"] == nil || string(sm.last.attachments["a.txt"]) != "附件内容" {
		t.Fatalf("附件：%v", sm.last.attachments)
	}
	// 纯文本
	callToolStr(t, tools[0], map[string]any{"to": "x@y.z", "subject": "S", "body": "纯文本"})
	if sm.last.html {
		t.Fatal("纯文本不应 HTML")
	}
	// 附件不存在静默跳过
	out = callToolStr(t, tools[0], map[string]any{
		"to": "x@y.z", "subject": "S", "body": "t", "attachmentFileName": "nope.txt",
	})
	if out != "邮件发送成功: x@y.z" || len(sm.last.attachments) != 0 {
		t.Fatalf("缺失附件跳过：%q %v", out, sm.last.attachments)
	}
	// 越界附件被沙箱拒绝后静默跳过
	out = callToolStr(t, tools[0], map[string]any{
		"to": "x@y.z", "subject": "S", "body": "t", "attachmentFileName": "../../etc/passwd",
	})
	if out != "邮件发送成功: x@y.z" {
		t.Fatalf("越界附件：%q", out)
	}
}

// ---- mount ----

func TestTalentMountSystem(t *testing.T) {
	work := t.TempDir()
	mgr, err := mount.NewMountManager(work)
	if err != nil {
		t.Fatal(err)
	}
	if mgr.Get("@workspace") == nil {
		t.Fatal("内置工作区挂载")
	}

	// 技能挂载：SKILL.md 扫描
	skillDir := filepath.Join(work, "skills", "deploy")
	os.MkdirAll(skillDir, 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\ndescription: 部署技能\nversion: '2'\n---\n# Deploy"), 0o644)
	src, _ := mount.NewFileMountSource(filepath.Join(work, "skills"))
	mnt, err := mount.NewMountBuilder().Alias("skills").Description("技能库").
		Type(mount.TypeSkills).Source(src).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(mnt); err != nil {
		t.Fatal(err)
	}
	sd := mgr.Skills().Find("deploy")
	if sd == nil || sd.ID != "@skills/deploy" || sd.Description != "部署技能" || sd.Version != "2" {
		t.Fatalf("技能目录：%+v", sd)
	}

	// 非法别名拒绝
	if _, err := mount.NewMountBuilder().Alias("bad/name").Source(src).Build(); err == nil {
		t.Fatal("非法别名应拒绝")
	}
	wsrc, _ := mount.NewFileMountSource(".")
	if _, err := mount.NewMountBuilder().Alias("@workspace").Source(wsrc).Build(); err != nil {
		t.Fatalf("Build 允许（保留名在 Register 拒绝）：%v", err)
	}
	ws2, _ := mount.NewMountBuilder().Alias("@workspace").Source(wsrc).Build()
	if err := mgr.Register(ws2); err == nil {
		t.Fatal("Register 应拒 @workspace")
	}

	// 逻辑路径解析
	rr, err := mgr.ResolveResource("@skills/deploy/SKILL.md")
	if err != nil || rr.MountAlias != "@skills" || rr.SourcePath != "deploy/SKILL.md" {
		t.Fatalf("解析：%+v %v", rr, err)
	}
	// 相对路径 → @workspace
	rr, _ = mgr.ResolveResource("a/b.txt")
	if rr.MountAlias != "@workspace" || rr.SourcePath != "a/b.txt" {
		t.Fatalf("工作区解析：%+v", rr)
	}

	// 代理目录
	agentDir := filepath.Join(work, "agents")
	os.MkdirAll(filepath.Join(agentDir, "reviewer"), 0o755)
	os.WriteFile(filepath.Join(agentDir, "reviewer", "AGENT.md"),
		[]byte("---\ndescription: 评审代理\n---\n"), 0o644)
	asrc, _ := mount.NewFileMountSource(agentDir)
	amt, _ := mount.NewMountBuilder().Alias("agents").Type(mount.TypeAgents).Source(asrc).Build()
	mgr.Register(amt)
	ad := mgr.Agents().Find("reviewer")
	if ad == nil || ad.Description != "评审代理" {
		t.Fatalf("代理目录：%+v", ad)
	}

	// 移除后目录清空
	mgr.Remove("@skills")
	if mgr.Skills().Find("deploy") != nil {
		t.Fatal("移除挂载后技能应消失")
	}
}

// ---- memory ----

func TestTalentMemoryStore(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.NewMemoryStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 写入 + 读取
	store.Put("shared", &memory.MemoryEntry{Key: "k1", Content: "用户偏好深色主题", Importance: 8, Scope: "user"})
	e := store.Get("shared", "k1")
	if e == nil || e.Content != "用户偏好深色主题" || e.Importance != 8 {
		t.Fatalf("读取：%+v", e)
	}
	// TTL 分档
	if memory.ComputeTTL(10) != -1 || memory.ComputeTTL(5) != 2592000 || memory.ComputeTTL(4) != 604800 {
		t.Fatal("TTL 分档")
	}

	// 重启加载
	store2, err := memory.NewMemoryStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := store2.Get("shared", "k1"); e == nil || e.Importance != 8 {
		t.Fatalf("重启加载：%+v", e)
	}

	// scope 覆盖序：user > workspace
	store.Put("shared", &memory.MemoryEntry{Key: "dup", Content: "用户域", Importance: 5, Scope: "user"})
	store.Put("shared", &memory.MemoryEntry{Key: "dup", Content: "工作域", Importance: 5, Scope: "workspace"})
	if e := store.Get("shared", "dup"); e.Scope != "user" {
		t.Fatalf("覆盖序：%+v", e)
	}

	// 检索：中文 bi-gram 命中
	store.Put("shared", &memory.MemoryEntry{Key: "框架", Content: "项目使用 Spring 框架开发", Importance: 6, Scope: "user"})
	store.Put("shared", &memory.MemoryEntry{Key: "饮食", Content: "用户喜欢川菜", Importance: 5, Scope: "user"})
	hits := store.Search("shared", "框架", 5)
	if len(hits) == 0 || hits[0].Key != "框架" {
		t.Fatalf("中文检索：%v", hits)
	}
	// java21 整段 token
	store.Put("shared", &memory.MemoryEntry{Key: "jdk", Content: "环境是 java21", Importance: 5, Scope: "user"})
	hits = store.Search("shared", "java21", 5)
	if len(hits) == 0 {
		t.Fatal("整段 token 检索")
	}

	// Hot
	if hot := store.Hot("shared", 10); len(hot) == 0 || hot[0].Importance < 5 {
		t.Fatalf("Hot：%v", hot)
	}
}

func TestTalentMemoryTools(t *testing.T) {
	dir := t.TempDir()
	store, _ := memory.NewMemoryStore(dir)
	m := memory.NewMemory(store)
	tools := m.Tools(nil)
	byName := map[string]ai.FunctionTool{}
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	if len(tools) != 5 {
		t.Fatalf("五工具：%d", len(tools))
	}

	// extract → 【操作成功】前缀
	out := callToolStr(t, byName["memory_extract"], map[string]any{
		"key": "偏好", "fact": "喜欢深色主题", "importance": 8})
	if !strings.HasPrefix(out, "【操作成功】") {
		t.Fatalf("extract：%q", out)
	}
	// 同 Key 再写 → 认知对比
	out = callToolStr(t, byName["memory_extract"], map[string]any{
		"key": "偏好", "fact": "喜欢浅色主题", "importance": 8})
	if !strings.Contains(out, "[认知对比]") {
		t.Fatalf("对比：%q", out)
	}
	// search *
	out = callToolStr(t, byName["memory_search"], map[string]any{"query": "*"})
	if !strings.Contains(out, "(Key: 偏好)") {
		t.Fatalf("列出全部：%q", out)
	}
	// recall
	out = callToolStr(t, byName["memory_recall"], map[string]any{"key": "偏好"})
	if !strings.Contains(out, "喜欢浅色主题") {
		t.Fatalf("recall：%q", out)
	}
	// consolidate：来源可读校验
	out = callToolStr(t, byName["memory_consolidate"], map[string]any{
		"keys_to_merge": `["偏好","nope"]`, "new_key": "洞察", "evolved_insight": "颜色偏好"})
	if !strings.Contains(out, "【合并异常】") {
		t.Fatalf("来源校验：%q", out)
	}
	// 合法合并：清理来源 + 洞察前缀
	keys, _ := json.Marshal([]string{"偏好"})
	out = callToolStr(t, byName["memory_consolidate"], map[string]any{
		"keys_to_merge": string(keys), "new_key": "洞察", "evolved_insight": "颜色偏好随季节"})
	if !strings.Contains(out, "【操作成功】") || !strings.Contains(out, "已清理来源: 偏好") {
		t.Fatalf("合并：%q", out)
	}
	if e := store.Get("shared", "洞察"); e == nil || !strings.HasPrefix(e.Content, "[Evolved Insight] ") {
		t.Fatalf("洞察内容：%+v", e)
	}
	// prune
	out = callToolStr(t, byName["memory_prune"], map[string]any{"key": "洞察"})
	if out != "已清理 Key: 洞察" {
		t.Fatalf("prune：%q", out)
	}
	if out = callToolStr(t, byName["memory_prune"], map[string]any{"key": "不存在"}); !strings.Contains(out, "无需清理") {
		t.Fatalf("prune 缺失：%q", out)
	}
}

// ---- code ----

func TestTalentCodeProviders(t *testing.T) {
	providers := code.BuiltinProviders()
	if len(providers) < 6 {
		t.Fatalf("内置 Provider：%d", len(providers))
	}
	// Go Provider 形态
	var goP code.LanguageProvider
	for _, p := range providers {
		if p.ID() == "Go" {
			goP = p
		}
	}
	if goP == nil {
		t.Fatal("Go Provider 缺失")
	}
	entries := map[string]bool{"go.mod": true, "cmd": true}
	if !goP.IsMatch("/x", entries) {
		t.Fatal("go.mod 命中")
	}
	if goP.IsMatch("/x", map[string]bool{"package.json": true}) {
		t.Fatal("不应命中")
	}
	var sb strings.Builder
	goP.AppendRootCommands(&sb)
	if !strings.Contains(sb.String(), "go build ./...") || !strings.Contains(sb.String(), "go test ./...") {
		t.Fatalf("根命令：%s", sb.String())
	}
	var sb2 strings.Builder
	goP.AppendModuleCommands(&sb2, "svc")
	if !strings.Contains(sb2.String(), "cd svc && go build ./...") {
		t.Fatalf("模块命令：%s", sb2.String())
	}
	// 版本探测
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0o644)
	if v := goP.DetectVersion(dir); v != "Go 1.27" {
		t.Fatalf("版本探测：%q", v)
	}
}

func TestTalentCodeTalent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.27\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("# existing\n"), 0o644)
	// 子模块
	sub := filepath.Join(dir, "web")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "package.json"), []byte(`{"name":"web"}`), 0o644)

	ct := code.New(dir)
	if !ct.IsSupported(nil) {
		t.Fatal("go.mod 准入")
	}
	ct.OnAttach(nil)
	data, err := os.ReadFile(ct.CodeMdPath())
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "go mod tidy") || !strings.Contains(content, "cd web && npm install") ||
		!strings.Contains(content, "Go 1.27") || !strings.Contains(content, "## 工程规约") {
		t.Fatalf("CODE.md 内容：\n%s", content)
	}
	// gitignore 追加
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), ".soloncode") {
		t.Fatalf("gitignore：%s", gi)
	}
	// 指令注入
	if ins := ct.Instruction(nil); !strings.Contains(ins, "CODE.md") {
		t.Fatalf("指令：%s", ins)
	}
	if tools := ct.Tools(nil); tools != nil {
		t.Fatal("纯指令型无工具")
	}
}

// ---- data ----

type stubKV struct {
	m map[string]string
}

func (s *stubKV) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[key] = value
	return nil
}
func (s *stubKV) Get(ctx context.Context, key string) (string, bool, error) {
	v, ok := s.m[key]
	return v, ok, nil
}

func TestTalentData(t *testing.T) {
	kv := &stubKV{}
	d := data.New(kv, "team1")
	if d.Name() != "redis_storage" {
		t.Fatal("名称")
	}
	tools := d.Tools(nil)

	out := callToolStr(t, tools[0], map[string]any{"key": "里程碑", "value": "v1 上线"})
	if out != "我已经把这件事记在我的笔记里了（Key: 里程碑）。" {
		t.Fatalf("set 文案：%q", out)
	}
	if _, ok := kv.m["ai:memory:team1:里程碑"]; !ok {
		t.Fatalf("最终键：%v", kv.m)
	}
	out = callToolStr(t, tools[1], map[string]any{"key": "里程碑"})
	if out != "关于 [里程碑]，我记得是：v1 上线" {
		t.Fatalf("get 文案：%q", out)
	}
	out = callToolStr(t, tools[1], map[string]any{"key": "缺失"})
	if out != "我在笔记里没找到关于 [缺失] 的内容。" {
		t.Fatalf("缺失文案：%q", out)
	}
}

// newWebhookServer webhook 回放服务（记录 body/path；返回 *httptest.Server）。
func newWebhookServer(t *testing.T, body *string, urlPath *string, reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*body = string(raw)
		*urlPath = r.URL.String()
		io.WriteString(w, reply)
	}))
	return srv
}
