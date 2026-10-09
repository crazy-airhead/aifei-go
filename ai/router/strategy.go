package router

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// RoundRobinStrategy 轮询（对标 RoundRobinRoutingStrategy）：原子游标取模。
type RoundRobinStrategy struct {
	cursor atomic.Int64
}

// Select 游标自增取模路由表。
func (s *RoundRobinStrategy) Select(_ *Context, routes []Route) (Decision, error) {
	if len(routes) == 0 {
		return Decision{}, errors.New("router: the routes are required")
	}
	n := s.cursor.Add(1)
	return Decision{RouteID: routes[int((n-1)%int64(len(routes)))].ID(), Reasoning: "round-robin"}, nil
}

// WeightedRoundRobinStrategy 平滑加权轮询（对标 WeightedRoundRobinRoutingStrategy，
// nginx smooth WRR）：current[id] += weight 后取最大者，胜者回扣 totalWeight。
// 拓扑（id+权重序）首次使用后冻结——变了说明调用方在换路由表，静默错配不如报错。
type WeightedRoundRobinStrategy struct {
	mu          sync.Mutex
	initialized bool
	routeIDs    []string
	weights     []int
	current     map[string]int64
}

// Select 同步执行（对标 Java synchronized select）。
func (s *WeightedRoundRobinStrategy) Select(_ *Context, routes []Route) (Decision, error) {
	if len(routes) == 0 {
		return Decision{}, errors.New("router: the routes are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		s.initialize(routes)
	} else if err := s.verifyTopology(routes); err != nil {
		return Decision{}, err
	}

	selected := -1
	selectedWeight := int64(math.MinInt64)
	var totalWeight int64
	for i, route := range routes {
		cw := s.current[route.ID()] + int64(route.Weight())
		s.current[route.ID()] = cw
		totalWeight += int64(route.Weight())
		if selected < 0 || cw > selectedWeight {
			selected, selectedWeight = i, cw
		}
	}
	s.current[routes[selected].ID()] = selectedWeight - totalWeight
	return Decision{RouteID: routes[selected].ID(), Reasoning: "smooth weighted round-robin"}, nil
}

func (s *WeightedRoundRobinStrategy) initialize(routes []Route) {
	s.routeIDs = make([]string, len(routes))
	s.weights = make([]int, len(routes))
	s.current = make(map[string]int64, len(routes))
	for i, route := range routes {
		s.routeIDs[i] = route.ID()
		s.weights[i] = route.Weight()
		s.current[route.ID()] = 0
	}
	s.initialized = true
}

func (s *WeightedRoundRobinStrategy) verifyTopology(routes []Route) error {
	if len(routes) != len(s.routeIDs) {
		return errors.New("router: the weighted routes cannot change after first use")
	}
	for i, route := range routes {
		if route.ID() != s.routeIDs[i] || route.Weight() != s.weights[i] {
			return errors.New("router: the weighted routes cannot change after first use")
		}
	}
	return nil
}

// RuleBasedStrategy 规则路由（对标 RuleBasedRoutingStrategy）：有序规则，
// 首个谓词命中即定案；全不命中报错（无兜底——兜底应表达为最后一条规则）。
type RuleBasedStrategy struct {
	rules []Rule
}

// NewRuleBasedStrategy 校验并构建（规则非空、无 nil 谓词——NewRule 已保证单元合法）。
func NewRuleBasedStrategy(rules []Rule) (*RuleBasedStrategy, error) {
	if len(rules) == 0 {
		return nil, errors.New("router: the routing rules are required")
	}
	return &RuleBasedStrategy{rules: append([]Rule(nil), rules...)}, nil
}

// Rules 规则表（保持构建序）。
func (s *RuleBasedStrategy) Rules() []Rule { return append([]Rule(nil), s.rules...) }

// Select 依序探测，首个命中胜出。
func (s *RuleBasedStrategy) Select(ctx *Context, routes []Route) (Decision, error) {
	if ctx == nil {
		return Decision{}, errors.New("router: the routing context is required")
	}
	for _, rule := range s.rules {
		if rule.Matches(ctx) {
			return Decision{RouteID: rule.RouteID(), Reasoning: "matched rule: " + rule.RouteID()}, nil
		}
	}
	return Decision{}, errors.New("router: no routing rule matched")
}

// SmartStrategy 智能路由（对标 SmartRoutingStrategy）：以分类聊天模型做 LLM 决策。
// 分类器带 outputSchema 的结构化输出，回读 Decision 并校验（routeId 非空且在
// 候选表内、reasoning 非空）。
type SmartStrategy struct {
	classifierModel *ai.ChatModel
}

// NewSmartStrategy 构建分类策略（分类模型非 nil）。
func NewSmartStrategy(classifierModel *ai.ChatModel) (*SmartStrategy, error) {
	if classifierModel == nil {
		return nil, errors.New("router: the classifier model is required")
	}
	return &SmartStrategy{classifierModel: classifierModel}, nil
}

// Select 分类模型决策；调用/解析失败包装为路由错误。
func (s *SmartStrategy) Select(ctx *Context, routes []Route) (Decision, error) {
	if ctx == nil {
		return Decision{}, errors.New("router: the routing context is required")
	}
	if len(routes) == 0 {
		return Decision{}, errors.New("router: the routes are required")
	}

	resp, err := s.classifierModel.
		Prompt(buildClassifierPrompt(ctx, routes)).
		Options(func(o *ai.ChatOptions) {
			o.OutputSchema = ai.OutputSchemaOf[Decision]()
		}).
		Call()
	if err != nil {
		return Decision{}, fmt.Errorf("router: smart routing classification failed: %w", err)
	}
	if resp == nil || resp.Message() == nil {
		return Decision{}, errors.New("router: the classifier response message is required")
	}

	decision, err := ai.Bind[Decision](resp.Message())
	if err != nil {
		return Decision{}, fmt.Errorf("router: smart routing classification failed: %w", err)
	}
	if err := validateDecision(decision, routes); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

// buildClassifierPrompt 分类器提示（对标 Java buildClassifierPrompt 逐段）：
// 任务说明 + 候选清单（id: description）+ 结构化约束 + 原始消息投影（role: content）。
func buildClassifierPrompt(ctx *Context, routes []Route) *ai.Prompt {
	var buf strings.Builder
	buf.WriteString("You are a routing classifier. Select only one candidate route id " +
		"and explain the reason.\n\nCandidate routes:\n")
	for _, route := range routes {
		buf.WriteString("- " + route.ID() + ": " + route.Description() + "\n")
	}
	buf.WriteString("\nReturn a structured decision whose routeId is exactly one candidate id " +
		"and whose reasoning is non-empty.\n\nOriginal messages:\n")
	for _, message := range ctx.Prompt().Messages() {
		buf.WriteString(string(message.Role) + ": ")
		buf.WriteString(messageContentText(message))
		buf.WriteString("\n")
	}
	return ai.PromptOf(buf.String())
}

// messageContentText 消息内容投影（Java getContent() 的 toString 语义：字符串
// 原样，其余结构给可读形态）。
func messageContentText(m *ai.Message) string {
	if m.Content == nil {
		return ""
	}
	if s, ok := m.Content.(string); ok {
		return s
	}
	if am, ok := m.Content.(*ai.AssistantMessage); ok {
		return am.Text
	}
	if tm, ok := m.Content.(*ai.ToolMessage); ok {
		return tm.ResultText()
	}
	return fmt.Sprint(m.Content)
}

// validateDecision 决策校验：routeId/reasoning 非空，routeId 在候选表内。
func validateDecision(decision Decision, routes []Route) error {
	if strings.TrimSpace(decision.RouteID) == "" {
		return errors.New("router: the classifier routeId is required")
	}
	if strings.TrimSpace(decision.Reasoning) == "" {
		return errors.New("router: the classifier reasoning is required")
	}
	for _, route := range routes {
		if route.ID() == decision.RouteID {
			return nil
		}
	}
	return fmt.Errorf("router: unknown classifier route id: %s", decision.RouteID)
}
