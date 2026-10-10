// Package memory 长期记忆能力包（对标 MemoryTalent + MemoryMdData 的 md 方案：
// 首卡落内存索引 + md 文件持久——scope 目录（user > workspace 覆盖序）、
// 原子写、CJK bi-gram 分词 + idf² 评分、TTL 分档、五工具 extract/search/
// recall/consolidate/prune）。
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// MemoryEntry 记忆条目。
type MemoryEntry struct {
	Key        string    `json:"key"`
	Content    string    `json:"content"`
	Time       time.Time `json:"time"`
	Importance int       `json:"importance"`
	TTLSec     int       `json:"ttl"`
	Scope      string    `json:"scope"`
	StoredAt   time.Time `json:"stored_at"`
}

// TTL 分档（importance≥10 永久；≥5 30 天；其余 7 天——Java computeTtl 同款）。
func ComputeTTL(importance int) int {
	switch {
	case importance >= 10:
		return -1
	case importance >= 5:
		return 2592000
	default:
		return 604800
	}
}

// MemoryStore 存储面（md 文件 + 内存索引）。
type MemoryStore struct {
	mu         sync.RWMutex
	baseDir    string
	scopeOrder []string            // 覆盖优先序（前高后低）
	entries    map[string][]*MemoryEntry // userId → entries（Key 唯一）
}

// NewMemoryStore 构造（baseDir 承载 {scope}/{userId}__{key}.md；scopeOrder
// 缺省 [user, workspace]——前优先）。
func NewMemoryStore(baseDir string, scopeOrder ...string) (*MemoryStore, error) {
	if len(scopeOrder) == 0 {
		scopeOrder = []string{"user", "workspace"}
	}
	s := &MemoryStore{baseDir: baseDir, scopeOrder: scopeOrder, entries: map[string][]*MemoryEntry{}}
	if err := s.loadAll(); err != nil {
		return nil, err
	}
	return s, nil
}

// 文件形态：{scope}/{userId}__{key}.md，front matter + 正文 content。
var fmLine = regexp.MustCompile(`^(\w+):\s*(.*)$`)

func (s *MemoryStore) pathOf(scope, userID, key string) string {
	return filepath.Join(s.baseDir, scope, userID+"__"+sanitizeKey(key)+".md")
}

func sanitizeKey(k string) string {
	// key 禁路径分隔符（防逃逸）
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '_'
		}
		return r
	}, k)
}

func (s *MemoryStore) loadAll() error {
	return filepath.Walk(s.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, err := filepath.Rel(s.baseDir, path)
		if err != nil {
			return nil
		}
		parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
		if len(parts) != 2 {
			return nil
		}
		scope := parts[0]
		name := strings.TrimSuffix(parts[1], ".md")
		idx := strings.Index(name, "__")
		if idx < 0 {
			return nil
		}
		userID, key := name[:idx], name[idx+2:]
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		entry := parseEntry(string(data))
		if entry == nil {
			return nil
		}
		entry.Key, entry.Scope = key, scope
		s.appendEntry(userID, entry)
		// 过期删除
		if entry.TTLSec > 0 && time.Since(entry.StoredAt) > time.Duration(entry.TTLSec)*time.Second {
			_ = os.Remove(path)
			s.removeEntry(userID, key)
		}
		return nil
	})
}

func parseEntry(content string) *MemoryEntry {
	if !strings.HasPrefix(content, "---\n") {
		return nil
	}
	rest := content[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil
	}
	e := &MemoryEntry{}
	for _, line := range strings.Split(rest[:end], "\n") {
		m := fmLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch m[1] {
		case "time":
			e.Time, _ = time.Parse("2006-01-02 15:04:05", strings.TrimSpace(m[2]))
		case "importance":
			fmt.Sscanf(strings.TrimSpace(m[2]), "%d", &e.Importance)
		case "ttl":
			fmt.Sscanf(strings.TrimSpace(m[2]), "%d", &e.TTLSec)
		case "stored_at":
			e.StoredAt, _ = time.Parse(time.RFC3339, strings.TrimSpace(m[2]))
		}
	}
	body := strings.TrimLeft(rest[end+4:], "\r\n")
	e.Content = body
	return e
}

func (e *MemoryEntry) serialize() string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: __entry__\n")
	sb.WriteString("time: " + e.Time.Format("2006-01-02 15:04:05") + "\n")
	sb.WriteString(fmt.Sprintf("importance: %d\n", e.Importance))
	sb.WriteString(fmt.Sprintf("ttl: %d\n", e.TTLSec))
	sb.WriteString("stored_at: " + e.StoredAt.Format(time.RFC3339) + "\n")
	sb.WriteString("scope: " + e.Scope + "\n")
	sb.WriteString("---\n\n")
	sb.WriteString(e.Content)
	return sb.String()
}

func (s *MemoryStore) appendEntry(userID string, e *MemoryEntry) {
	list := s.entries[userID]
	for i, existing := range list {
		if existing.Key == e.Key && existing.Scope == e.Scope {
			list[i] = e
			return
		}
	}
	s.entries[userID] = append(list, e)
}

func (s *MemoryStore) removeEntry(userID, key string) {
	out := s.entries[userID][:0]
	for _, e := range s.entries[userID] {
		if e.Key != key {
			out = append(out, e)
		}
	}
	s.entries[userID] = out
}

// Put 写入（原子写 + fail-on-error——Java put 同款）。
func (s *MemoryStore) Put(userID string, e *MemoryEntry) error {
	if e == nil || e.Key == "" {
		return fmt.Errorf("memory: key is required")
	}
	if e.TTLSec == 0 {
		e.TTLSec = ComputeTTL(e.Importance)
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.StoredAt = time.Now()

	path := s.pathOf(e.Scope, userID, e.Key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(e.serialize()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	s.mu.Lock()
	s.appendEntry(userID, e)
	s.mu.Unlock()
	return nil
}

// Get 读取（scope 覆盖序：user > workspace）。
func (s *MemoryStore) Get(userID, key string) *MemoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, scope := range s.scopeOrder {
		for _, e := range s.entries[userID] {
			if e.Key == key && e.Scope == scope {
				if e.TTLSec > 0 && time.Since(e.StoredAt) > time.Duration(e.TTLSec)*time.Second {
					return nil
				}
				return e
			}
		}
	}
	return nil
}

// Remove 删除（fail-on-error）。
func (s *MemoryStore) Remove(userID, key string) error {
	s.mu.Lock()
	entry := s.getLocked(userID, key)
	if entry != nil {
		s.removeEntry(userID, key)
	}
	s.mu.Unlock()
	if entry == nil {
		return nil
	}
	path := s.pathOf(entry.Scope, userID, key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *MemoryStore) getLocked(userID, key string) *MemoryEntry {
	for _, scope := range s.scopeOrder {
		for _, e := range s.entries[userID] {
			if e.Key == key && e.Scope == scope {
				return e
			}
		}
	}
	return nil
}

// ListAll 全部（'*' 约定符形态）。
func (s *MemoryStore) ListAll(userID string, limit int) []*MemoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*MemoryEntry
	for _, e := range s.entries[userID] {
		if e.TTLSec > 0 && time.Since(e.StoredAt) > time.Duration(e.TTLSec)*time.Second {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Hot 高重要度（Imp ≥ 5，降序 + 时间倒序）。
func (s *MemoryStore) Hot(userID string, limit int) []*MemoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*MemoryEntry
	for _, e := range s.entries[userID] {
		if e.Importance >= 5 && (e.TTLSec < 0 || time.Since(e.StoredAt) <= time.Duration(e.TTLSec)*time.Second) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Importance != out[j].Importance {
			return out[i].Importance > out[j].Importance
		}
		return out[i].Time.After(out[j].Time)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Search 检索（CJK bi-gram + ASCII 整段 token；idf² 精确命中阶段 + 子串兜底
// 阶段两段值域隔离——Java 算法同款）。
func (s *MemoryStore) Search(userID, query string, limit int) []*MemoryEntry {
	entries := s.ListAll(userID, 0)
	if limit <= 0 {
		limit = 10
	}
	qTokens := Tokenize(query)
	if len(qTokens) == 0 {
		return nil
	}
	// 文档频率
	n := float64(len(entries))
	df := map[string]int{}
	docTokens := make([]map[string]bool, len(entries))
	for i, e := range entries {
		toks := map[string]bool{}
		for _, t := range Tokenize(e.Content + " " + e.Key) {
			toks[t] = true
		}
		docTokens[i] = toks
		for t := range toks {
			df[t]++
		}
	}
	idf := func(token string) float64 { return math.Log((n+1)/(float64(df[token])+1)) + 1 }

	type scored struct {
		entry *MemoryEntry
		score float64
	}
	var exact, substr []scored
	maxIDF2 := 0.0
	for _, t := range qTokens {
		if v := idf(t); v > maxIDF2 {
			maxIDF2 = v
		}
	}
	denom := float64(len(qTokens)) * maxIDF2 * maxIDF2
	if denom <= 0 {
		denom = 1
	}
	qLower := strings.ToLower(query)
	for i, e := range entries {
		hitScore := 0.0
		matched := 0
		for _, t := range qTokens {
			if docTokens[i][t] {
				idfV := idf(t)
				hitScore += idfV * idfV
				matched++
			}
		}
		if hitScore > 0 {
			norm := hitScore / denom
			exact = append(exact, scored{e, 0.5 + 0.5*norm})
			continue
		}
		// 子串兜底（0.45 × 命中比）
		lower := strings.ToLower(e.Content + " " + e.Key)
		subMatched := 0
		for _, t := range qTokens {
			if len(t) > 1 && strings.Contains(lower, t) {
				subMatched++
			}
		}
		if subMatched > 0 {
			substr = append(substr, scored{e, 0.45 * float64(subMatched) / float64(len(qTokens))})
		}
		_ = qLower
	}
	var all []scored
	all = append(all, exact...)
	all = append(all, substr...)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		if all[i].entry.Importance != all[j].entry.Importance {
			return all[i].entry.Importance > all[j].entry.Importance
		}
		return all[i].entry.Time.After(all[j].entry.Time)
	})
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]*MemoryEntry, len(all))
	for i, sc := range all {
		out[i] = sc.entry
	}
	return out
}

// Tokenize 语言无关分词（ASCII 字母数字整段（len>1）+ CJK bi-gram + 完整短语
// + 单字——Java tokenize 同款）。
func Tokenize(text string) []string {
	var tokens []string
	var ascii strings.Builder
	runes := []rune(strings.ToLower(text))
	flushASCII := func() {
		s := ascii.String()
		if len(s) > 1 {
			tokens = append(tokens, s)
		}
		ascii.Reset()
	}
	var cjkRunes []rune
	flushCJK := func() {
		if len(cjkRunes) == 0 {
			return
		}
		phrase := string(cjkRunes)
		if len(cjkRunes) > 1 {
			tokens = append(tokens, phrase)
			for i := 0; i+2 <= len(cjkRunes); i++ {
				tokens = append(tokens, string(cjkRunes[i:i+2]))
			}
		}
		for _, r := range cjkRunes {
			tokens = append(tokens, string(r))
		}
		cjkRunes = nil
	}
	for _, r := range runes {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			flushCJK()
			ascii.WriteRune(r)
		case r >= 0x3400 && r <= 0x9FFF || r >= 0xF900 && r <= 0xFAFF || r >= 0x3040 && r <= 0x30FF:
			flushASCII()
			cjkRunes = append(cjkRunes, r)
		default:
			flushASCII()
			flushCJK()
		}
	}
	flushASCII()
	flushCJK()
	return tokens
}

// ---- Talent ----

// Memory 能力包。
type Memory struct {
	ai.TalentBase
	store *MemoryStore
	defaultScope string
	sessionIsolation bool
	relevanceCount  int
	priorityCount   int
	summaryLength   int
}

// NewMemory 构造（sessionIsolation=false → userId 恒 shared——Java 同款）。
func NewMemory(store *MemoryStore) *Memory {
	return &Memory{
		store: store, defaultScope: "workspace", sessionIsolation: false,
		relevanceCount: 5, priorityCount: 5, summaryLength: 80,
	}
}

// SessionIsolation 会话隔离开关。
func (t *Memory) SessionIsolation(v bool) *Memory { t.sessionIsolation = v; return t }

// RelevanceCount 语义检索注入条数。
func (t *Memory) RelevanceCount(n int) *Memory { t.relevanceCount = n; return t }

// Name 名称。
func (t *Memory) Name() string { return "Memory" }

// Description 描述。
func (t *Memory) Description() string {
	return "长期记忆专家：负责用户心智模型的提取、演进、冲突消解与深度检索"
}

// Tools 五工具。
func (t *Memory) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("memory_extract").
			Desc("存入或更新跨会话可复用的事实、偏好和经验。不得存储敏感凭据。").
			StringParam("key", "记忆键（简短标识符；同主题请复用已有 Key）", true).
			StringParam("fact", "记忆内容", true).
			IntParam("importance", "重要度 1-10（1-4 待验证 7 天；5-6 可信 30 天；7-9 稳定 30 天；10 永久）", true).
			StringParam("scope", "作用域（user/workspace，默认沿用同 Key 旧域）", false).
			Handler(t.toolExtract).
			Build(),
		ai.NewTool("memory_search").
			Desc("检索记忆（'*' 列出全部）").
			StringParam("query", "查询词（或 *）", true).
			IntParam("topK", "返回条数（默认 5，上限 100）", false).
			Handler(t.toolSearch).
			Build(),
		ai.NewTool("memory_recall").
			Desc("按键精确读取记忆详情").
			StringParam("key", "记忆键", true).
			Handler(t.toolRecall).
			Build(),
		ai.NewTool("memory_consolidate").
			Desc("合并多条记忆为演进洞察").
			StringParam("keys_to_merge", "来源 Key 列表（JSON 数组）", true).
			StringParam("new_key", "新 Key", true).
			StringParam("evolved_insight", "演进后的洞察", true).
			StringParam("scope", "作用域（可选）", false).
			Handler(t.toolConsolidate).
			Build(),
		ai.NewTool("memory_prune").
			Desc("清理指定记忆").
			StringParam("key", "记忆键", true).
			Handler(t.toolPrune).
			Build(),
	}
}

func (t *Memory) userID(p *ai.Prompt) string {
	if t.sessionIsolation && p != nil {
		if id, ok := p.Attrs()["sessionId"].(string); ok && id != "" {
			return id
		}
	}
	return "shared"
}

func (t *Memory) toolExtract(ctx context.Context, args map[string]any) (any, error) {
	key, _ := args["key"].(string)
	fact, _ := args["fact"].(string)
	importance := clampImportance(intOf(args["importance"]))
	scope, _ := args["scope"].(string)

	// 未显式指定 scope 沿用同 Key 旧域
	userID := t.userID(nil)
	old := t.store.Get(userID, key)
	feedback := ""
	if old != nil {
		feedback = fmt.Sprintf("[认知对比] 旧内容: %s | 旧时间: %s\n", truncate(old.Content, 60), old.Time.Format("2006-01-02 15:04:05"))
		if scope == "" {
			scope = old.Scope
		}
	}
	if scope == "" {
		scope = t.defaultScope
	}
	entry := &MemoryEntry{Key: key, Content: fact, Importance: importance, Scope: scope}
	if err := t.store.Put(userID, entry); err != nil {
		return "存储失败: " + err.Error(), nil
	}
	out := feedback + "【操作成功】已记录 " + key
	// 碎片密度提示（Imp<5 碎片 ≥5 条）
	fragments := 0
	for _, e := range t.store.ListAll(userID, 0) {
		if e.Importance < 5 {
			fragments++
		}
	}
	if importance < 5 && fragments >= 5 {
		out += "\n提示：低重要度记忆碎片较多（" + fmt.Sprint(fragments) + " 条），建议用 memory_consolidate 合并。"
	}
	return out, nil
}

func (t *Memory) toolSearch(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	topK := intOf(args["topK"])
	if topK <= 0 {
		topK = 5
	}
	if topK > 100 {
		topK = 100
	}
	userID := t.userID(nil)
	var sb strings.Builder
	if strings.TrimSpace(query) == "*" {
		for _, e := range t.store.ListAll(userID, 100) {
			sb.WriteString(fmt.Sprintf("- [%s] [%s](Key: %s) Imp:%d: %s\n",
				e.Time.Format("2006-01-02 15:04:05"), e.Scope, e.Key, e.Importance, truncate(e.Content, 80)))
		}
		if sb.Len() == 0 {
			return "暂无记忆条目。", nil
		}
		return sb.String(), nil
	}
	for _, e := range t.store.Search(userID, query, topK) {
		sb.WriteString(fmt.Sprintf("- [%s] [%s](Key: %s) Imp:%d: %s\n",
			e.Time.Format("2006-01-02 15:04:05"), e.Scope, e.Key, e.Importance, e.Content))
	}
	if sb.Len() == 0 {
		return "未找到相关记忆。", nil
	}
	return sb.String(), nil
}

func (t *Memory) toolRecall(ctx context.Context, args map[string]any) (any, error) {
	key, _ := args["key"].(string)
	if e := t.store.Get(t.userID(nil), key); e != nil {
		return fmt.Sprintf("【认知详情】内容：%s | 记录时间：%s | 重要度：%d",
			e.Content, e.Time.Format("2006-01-02 15:04:05"), e.Importance), nil
	}
	return "未找到认知条目 [" + key + "]。", nil
}

func (t *Memory) toolConsolidate(ctx context.Context, args map[string]any) (any, error) {
	keysJSON, _ := args["keys_to_merge"].(string)
	newKey, _ := args["new_key"].(string)
	insight, _ := args["evolved_insight"].(string)
	scope, _ := args["scope"].(string)

	var keys []string
	if err := json.Unmarshal([]byte(keysJSON), &keys); err != nil {
		return "【合并异常】keys_to_merge 非合法 JSON 数组，未写入新洞察。", nil
	}
	userID := t.userID(nil)
	// 全部来源可读校验
	for _, k := range keys {
		if t.store.Get(userID, k) == nil {
			return fmt.Sprintf("【合并异常】来源 %s 未找到，未写入新洞察。", k), nil
		}
	}
	// 目标已存在且不在来源列表 → 拒绝覆盖
	if existing := t.store.Get(userID, newKey); existing != nil {
		covered := false
		for _, k := range keys {
			if k == newKey {
				covered = true
			}
		}
		if !covered {
			return "【合并异常】目标 Key 已存在且不在来源列表，拒绝覆盖。", nil
		}
	}
	// 派生上限 9（仅目标自身 Imp=10 原地升维时保留 10）
	importance := 8
	if old := t.store.Get(userID, newKey); old != nil && old.Importance == 10 {
		importance = 10
	} else {
		importance = minInt(importance, 9)
	}
	if scope == "" {
		scope = t.defaultScope
	}
	entry := &MemoryEntry{Key: newKey, Content: "[Evolved Insight] " + insight, Importance: importance, Scope: scope}
	if err := t.store.Put(userID, entry); err != nil {
		return "【合并异常】写入失败: " + err.Error(), nil
	}
	// 读回校验（一致才清理；Imp=10 来源跳过——protectedNotice）
	confirmed := t.store.Get(userID, newKey)
	if confirmed != nil && confirmed.Content == entry.Content && confirmed.Importance == entry.Importance {
		var pruned, skipped []string
		for _, k := range keys {
			if k == newKey {
				continue
			}
			if src := t.store.Get(userID, k); src != nil && src.Importance == 10 {
				skipped = append(skipped, k)
				continue
			}
			if err := t.store.Remove(userID, k); err == nil {
				pruned = append(pruned, k)
			}
		}
		out := "【操作成功】洞察已写入 " + newKey
		if len(pruned) > 0 {
			out += "，已清理来源: " + strings.Join(pruned, ", ")
		}
		if len(skipped) > 0 {
			out += "，保护来源(Imp=10): " + strings.Join(skipped, ", ")
		}
		return out, nil
	}
	return "【合并异常】读回校验失败，全部来源已保留。", nil
}

func (t *Memory) toolPrune(ctx context.Context, args map[string]any) (any, error) {
	key, _ := args["key"].(string)
	if t.store.Get(t.userID(nil), key) == nil {
		return "未找到认知条目 [" + key + "]，无需清理。", nil
	}
	if err := t.store.Remove(t.userID(nil), key); err != nil {
		return "清理失败: " + err.Error(), nil
	}
	return "已清理 Key: " + key, nil
}

func clampImportance(n int) int {
	if n < 1 {
		return 1
	}
	if n > 10 {
		return 10
	}
	return n
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
