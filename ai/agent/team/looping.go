// looping.go 协作死循环拦截器（对标 team.intercept.LoopingTeamInterceptor）：通过
// 回溯轨迹识别并阻断智能体间的无效重复——单点停滞（同一成员连续产出高度相似
// 内容）与序列嵌套（多成员固定序列「踢皮球」，如 A-B-A-B / A-B-C-A-B-C）。
package team

import (
	"strings"
	"unicode"
)

// LoopingTeamInterceptor 协作死循环拦截器（阈值可调，零值即启用）。
type LoopingTeamInterceptor struct {
	InterceptorBase

	// MinContentLength 最小检测长度（过滤 "OK"、"Done" 等短语干扰）。
	MinContentLength int
	// SimilarityThreshold 语义相似度阈值 (0.0 ~ 1.0)。
	SimilarityThreshold float64
	// ScanWindowSize 回溯检测的最大步数窗口。
	ScanWindowSize int
	// MaxRepeatAllowed 允许的最大重复次数。
	MaxRepeatAllowed int
}

// NewLoopingTeamInterceptor 缺省阈值（对标 Java 字段缺省值）。
func NewLoopingTeamInterceptor() *LoopingTeamInterceptor {
	return &LoopingTeamInterceptor{
		MinContentLength:    10,
		SimilarityThreshold: 0.95,
		ScanWindowSize:      10,
		MaxRepeatAllowed:    0,
	}
}

// Name 拦截器标识。
func (*LoopingTeamInterceptor) Name() string { return "LoopingTeamInterceptor" }

// ShouldSupervisorContinue 准入检查：检出死循环即熔断（资源耗尽防护）。
func (l *LoopingTeamInterceptor) ShouldSupervisorContinue(trace *Trace) bool {
	if l.IsLooping(trace) {
		return false
	}
	return true
}

// IsLooping 多策略循环检测（仅审计 Agent 产出，排除系统开销步骤；至少 4 步）。
func (l *LoopingTeamInterceptor) IsLooping(trace *Trace) bool {
	if trace == nil {
		return false
	}
	var agentRecords []*Record
	for _, r := range trace.Records() {
		if r.IsAgent() {
			agentRecords = append(agentRecords, r)
		}
	}

	n := len(agentRecords)
	// 步数不足以判定循环（至少需要 A-B-A-B 四步）
	if n < 4 {
		return false
	}

	lastRecord := agentRecords[n-1]
	content := lastRecord.Content()
	if len([]rune(content)) < l.minContentLength() {
		return false
	}

	// 策略 A：检测 Agent 自我复读
	if l.checkSelfLoop(agentRecords, n) {
		return true
	}

	// 策略 B：检测多步协同死锁 (A-B-A-B 或 A-B-C-A-B-C)
	return l.checkSequenceLoop(agentRecords, n)
}

// checkSelfLoop 检测同一 Agent 是否在原地踏步（窗口内回溯；内容演进即打破）。
func (l *LoopingTeamInterceptor) checkSelfLoop(records []*Record, n int) bool {
	last := records[n-1]
	repeatCount := 0
	limit := n - l.scanWindowSize()
	if limit < 0 {
		limit = 0
	}

	for i := n - 2; i >= limit; i-- {
		prev := records[i]
		if prev.Source() == last.Source() {
			if l.calculateSimilarity(prev.Content(), last.Content()) >= l.similarityThreshold() {
				repeatCount++
				if repeatCount > l.maxRepeatAllowed() {
					return true
				}
			} else {
				// 内容发生演进，单点循环打破
				break
			}
		}
	}
	return false
}

// checkSequenceLoop 检测协作序列是否进入循环节（len 2=AB 型, 3=ABC 型；
// 节点名与内容双重匹配）。
func (l *LoopingTeamInterceptor) checkSequenceLoop(records []*Record, n int) bool {
	for length := 2; length <= 3; length++ {
		if n < length*2 {
			continue
		}

		match := true
		for i := 0; i < length; i++ {
			current := records[n-1-i]
			previous := records[n-1-i-length]

			if current.Source() != previous.Source() ||
				l.calculateSimilarity(current.Content(), previous.Content()) < l.similarityThreshold() {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// calculateSimilarity 基于归一化编辑距离的相似度（rune 粒度，对标 Java 语义）。
func (l *LoopingTeamInterceptor) calculateSimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}

	n1 := loopNormalize(s1)
	n2 := loopNormalize(s2)
	if string(n1) == string(n2) {
		return 1.0
	}

	maxLen := len(n1)
	if len(n2) > maxLen {
		maxLen = len(n2)
	}
	if maxLen == 0 {
		return 0.0
	}

	distance := loopEditDistance(n1, n2)
	return 1.0 - float64(distance)/float64(maxLen)
}

// loopNormalize 降噪：移除空白及格式干扰 + 小写。
func loopNormalize(text string) []rune {
	if text == "" {
		return nil
	}
	return []rune(strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)))
}

// loopEditDistance Levenshtein 编辑距离（单行滚动数组）。
func loopEditDistance(s1, s2 []rune) int {
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}

	costs := make([]int, len(s2)+1)
	for j := 0; j <= len(s2); j++ {
		costs[j] = j
	}
	for i := 1; i <= len(s1); i++ {
		last := costs[0] // 左上角
		costs[0] = i
		for j := 1; j <= len(s2); j++ {
			temp := costs[j] // 上一行 j
			if s1[i-1] == s2[j-1] {
				costs[j] = last
			} else {
				deleted := costs[j]    // 上方（删）
				inserted := costs[j-1] // 左方（插）
				if inserted < deleted {
					deleted = inserted
				}
				if last < deleted {
					deleted = last
				}
				costs[j] = deleted + 1
			}
			last = temp
		}
	}
	return costs[len(s2)]
}

// ---- 阈值缺省（零值字段回退 Java 缺省） ----

func (l *LoopingTeamInterceptor) minContentLength() int {
	if l.MinContentLength <= 0 {
		return 10
	}
	return l.MinContentLength
}

func (l *LoopingTeamInterceptor) similarityThreshold() float64 {
	if l.SimilarityThreshold <= 0 {
		return 0.95
	}
	return l.SimilarityThreshold
}

func (l *LoopingTeamInterceptor) scanWindowSize() int {
	if l.ScanWindowSize <= 0 {
		return 10
	}
	return l.ScanWindowSize
}

func (l *LoopingTeamInterceptor) maxRepeatAllowed() int {
	if l.MaxRepeatAllowed < 0 {
		return 0
	}
	return l.MaxRepeatAllowed
}
