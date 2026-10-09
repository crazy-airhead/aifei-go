// session_file_test.go P1-d FileSession 测试：NDJSON 往返（system 不落盘）、
// RemoveLatest 同步重写、窗口截断委托、Clear 删文件、坏行宽容装载、落盘失败
// 记 LastError 且内存层照常。
package ai_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

func fileSessionMsgs() []*ai.Message {
	toolMsg := ai.OfTool(ai.ToolResultOf("你好!"), "echo", "call_1", false)
	return []*ai.Message{
		ai.OfSystem("你是助手"),
		ai.OfUser("hi"),
		{Role: ai.RoleAssistant, Content: &ai.AssistantMessage{
			ToolCalls: []ai.ToolCall{{Index: "idx:0", ID: "call_1", Name: "echo",
				ArgumentsStr: `{"word":"你好"}`, Arguments: map[string]any{"word": "你好"}}},
		}},
		toolMsg,
		ai.OfAssistant("完成了"),
	}
}

// assertFileLines 会话文件应恰含给定角色的非 system 消息（一行一条）。
func assertFileLines(t *testing.T, dir, id string, wantRoles ...ai.Role) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, id+".messages.ndjson"))
	if err != nil {
		t.Fatalf("读会话文件: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != len(wantRoles) {
		t.Fatalf("文件行数 = %d（%q），want %d", len(lines), raw, len(wantRoles))
	}
	for i, line := range lines {
		msg := &ai.Message{}
		if err := msg.UnmarshalJSON([]byte(line)); err != nil {
			t.Fatalf("第 %d 行不是合法消息 JSON：%q", i, line)
		}
		if msg.Role != wantRoles[i] {
			t.Fatalf("第 %d 行 role = %s，want %s", i, msg.Role, wantRoles[i])
		}
	}
}

// TestFileSessionRoundtrip 写入后重开同 id 会话：非 system 消息完整还原
// （含 toolCalls assistant + tool 消息与解析参数）。
func TestFileSessionRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := ai.NewFileSession(dir, "sess-1")
	s.AddMessage(fileSessionMsgs()...)

	r := ai.NewFileSession(dir, "sess-1")
	if r.SessionID() != "sess-1" {
		t.Fatalf("SessionID = %q", r.SessionID())
	}
	msgs := r.Messages()
	if len(msgs) != 4 { // system 不落盘
		t.Fatalf("重载消息数 = %d，want 4（system 不持久化）：%v", len(msgs), msgs)
	}
	wantRoles := []ai.Role{ai.RoleUser, ai.RoleAssistant, ai.RoleTool, ai.RoleAssistant}
	for i, m := range msgs {
		if m.Role != wantRoles[i] {
			t.Fatalf("第 %d 条 role = %s，want %s", i, m.Role, wantRoles[i])
		}
	}
	am, ok := msgs[1].Content.(*ai.AssistantMessage)
	if !ok || len(am.ToolCalls) != 1 || am.ToolCalls[0].ID != "call_1" ||
		am.ToolCalls[0].Arguments["word"] != "你好" {
		t.Fatalf("toolCalls assistant 还原不符：%+v", am)
	}
	if r.LastError() != nil {
		t.Fatalf("LastError = %v", r.LastError())
	}
}

// TestFileSessionRemoveLatestSyncsDisk RemoveLatestMessage 后磁盘全量重写
// （toolCalls assistant 连带 tool 消息成组删除）。
func TestFileSessionRemoveLatestSyncsDisk(t *testing.T) {
	dir := t.TempDir()
	s := ai.NewFileSession(dir, "sess-2")
	s.AddMessage(fileSessionMsgs()...)

	s.RemoveLatestMessage(2) // “完成了” + toolCalls 组（assistant+tool 连带）
	assertFileLines(t, dir, "sess-2", ai.RoleUser)

	r := ai.NewFileSession(dir, "sess-2")
	if got := r.Messages(); len(got) != 1 || got[0].Role != ai.RoleUser {
		t.Fatalf("重载消息 = %v，应只剩 user", got)
	}
}

// TestFileSessionWindowTruncation 窗口截断与 RemoveLatest 委托 MemorySession 语义：
// 截断起点向前对齐 user；末尾 tool 组安全删除。
func TestFileSessionWindowTruncation(t *testing.T) {
	dir := t.TempDir()
	s := ai.NewFileSession(dir, "sess-3")
	s.AddMessage(
		ai.OfUser("q1"), ai.OfAssistant("a1"),
		ai.OfUser("q2"), ai.OfAssistant("a2"),
		ai.OfUser("q3"), ai.OfAssistant("a3"),
	)

	got := s.LatestMessages(2)
	if len(got) != 2 || got[0].Content != "q3" || got[1].Content != "a3" {
		t.Fatalf("LatestMessages(2) = %v", got)
	}
	// window 3 从 assistant a2 起截 → 向前对齐到 user q2
	got = s.LatestMessages(3)
	if len(got) != 4 || got[0].Content != "q2" {
		t.Fatalf("LatestMessages(3) = %v，应向前对齐 user", got)
	}

	s.RemoveLatestMessage(1)
	if got := s.Messages(); len(got) != 5 || got[len(got)-1].Content != "q3" {
		t.Fatalf("RemoveLatest 后 = %v", got)
	}
	assertFileLines(t, dir, "sess-3",
		ai.RoleUser, ai.RoleAssistant, ai.RoleUser, ai.RoleAssistant, ai.RoleUser)
}

// TestFileSessionClear Clear 清内存并删文件（重开为空会话）。
func TestFileSessionClear(t *testing.T) {
	dir := t.TempDir()
	s := ai.NewFileSession(dir, "sess-4")
	s.AddMessage(ai.OfUser("hi"))
	s.Clear()
	if !s.IsEmpty() {
		t.Fatalf("Clear 后应空")
	}
	if _, err := os.Stat(filepath.Join(dir, "sess-4.messages.ndjson")); !os.IsNotExist(err) {
		t.Fatalf("会话文件应被删除")
	}
	// 再次 Clear（无文件）不报错
	s.Clear()
	if s.LastError() != nil {
		t.Fatalf("LastError = %v", s.LastError())
	}
}

// TestFileSessionBadLinesTolerated 坏行（截断的半行/非法 JSON）跳过装载，
// 好行照常可用并记 LastError。
func TestFileSessionBadLinesTolerated(t *testing.T) {
	dir := t.TempDir()
	s := ai.NewFileSession(dir, "sess-5")
	s.AddMessage(ai.OfUser("好行"))
	raw, _ := os.ReadFile(filepath.Join(dir, "sess-5.messages.ndjson"))
	_ = os.WriteFile(filepath.Join(dir, "sess-5.messages.ndjson"),
		append(raw, []byte(`{"role":"user","content":"截断的半行`)...), 0o644)

	r := ai.NewFileSession(dir, "sess-5")
	if got := r.Messages(); len(got) != 1 || got[0].Content != "好行" {
		t.Fatalf("宽容装载 = %v，应只有好行", got)
	}
	if r.LastError() == nil {
		t.Fatalf("坏行应记 LastError")
	}
}

// TestFileSessionPersistFailure 落盘失败（目标路径被目录占位，rename 必败）：
// 内存层照常工作、错误进 LastError、不留临时文件。
func TestFileSessionPersistFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sess-6.messages.ndjson")
	if err := os.Mkdir(target, 0o755); err != nil { // 目标被目录占位
		t.Fatalf("mkdir 占位: %v", err)
	}
	s := ai.NewFileSession(dir, "sess-6")
	s.AddMessage(ai.OfUser("hi"))
	if got := s.Messages(); len(got) != 1 {
		t.Fatalf("落盘失败不应影响内存层：%v", got)
	}
	if s.LastError() == nil {
		t.Fatalf("落盘失败应记 LastError")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".aifei-session-") {
			t.Fatalf("临时文件残留：%s", e.Name())
		}
	}
}

// TestFileSessionRandomID 缺省 id 随机生成且文件随 id 落盘。
func TestFileSessionRandomID(t *testing.T) {
	dir := t.TempDir()
	s := ai.NewFileSession(dir)
	if s.SessionID() == "" {
		t.Fatalf("缺省 id 应随机生成")
	}
	s.AddMessage(ai.OfUser("hi"))
	if _, err := os.Stat(filepath.Join(dir, s.SessionID()+".messages.ndjson")); err != nil {
		t.Fatalf("会话文件应按 id 落盘: %v", err)
	}
}
