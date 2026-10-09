// Package router 聊天模型路由器（对标 solon-ai-router）：N 个候选模型按策略择一。
// 显式标注（Prompt attrs 的 AttrRouteID）优先于策略选择；策略家族四件套——
// RoundRobin / 加权（smooth WRR）/ 规则 / Smart（LLM 决策）。
package router

import (
	"errors"
	"fmt"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// AttrRouteID 显式路由标注键（值须为非空字符串）。沿用 Java 原值
// "solon.ai.router.routeId"，跨语言共享的 Prompt 标注可互认。
const AttrRouteID = "solon.ai.router.routeId"

// Decision 路由决策（Smart 策略的结构化输出目标，json 字段名与 Java 对齐）。
type Decision struct {
	RouteID   string `json:"routeId"`
	Reasoning string `json:"reasoning"`
}

// Route 候选路由：id + 描述（Smart 分类器的候选清单素材）+ 权重（>0，WRR 用）
// + 目标聊天模型。
type Route struct {
	id          string
	description string
	weight      int
	chatModel   *ai.ChatModel
}

// NewRoute 校验并构建（id/描述非空、权重 > 0、模型非 nil）。
func NewRoute(id, description string, weight int, chatModel *ai.ChatModel) (Route, error) {
	if strings.TrimSpace(id) == "" {
		return Route{}, errors.New("router: the route id is required")
	}
	if strings.TrimSpace(description) == "" {
		return Route{}, errors.New("router: the route description is required")
	}
	if weight <= 0 {
		return Route{}, errors.New("router: the route weight must be greater than zero")
	}
	if chatModel == nil {
		return Route{}, errors.New("router: the route chatModel is required")
	}
	return Route{id: id, description: description, weight: weight, chatModel: chatModel}, nil
}

func (r Route) ID() string               { return r.id }
func (r Route) Description() string      { return r.description }
func (r Route) Weight() int              { return r.weight }
func (r Route) ChatModel() *ai.ChatModel { return r.chatModel }

// Context 路由上下文：策略可见的 Prompt 快照（构造即拷贝，策略改不动原始提示）。
type Context struct {
	prompt *ai.Prompt
}

// NewContext 构建路由上下文（prompt 非 nil）。
func NewContext(prompt *ai.Prompt) (*Context, error) {
	if prompt == nil {
		return nil, errors.New("router: the routing prompt is required")
	}
	return &Context{prompt: prompt.Copy()}, nil
}

// Prompt 上下文提示的拷贝（copy-before-mutate，双方各自持副本）。
func (c *Context) Prompt() *ai.Prompt { return c.prompt.Copy() }

// Rule 规则路由的单元：routeId + 谓词（首个命中胜出）。
type Rule struct {
	routeID   string
	predicate func(*Context) bool
}

// NewRule 校验并构建（routeId 非空、谓词非 nil）。
func NewRule(routeID string, predicate func(*Context) bool) (Rule, error) {
	if strings.TrimSpace(routeID) == "" {
		return Rule{}, errors.New("router: the rule routeId is required")
	}
	if predicate == nil {
		return Rule{}, errors.New("router: the rule predicate is required")
	}
	return Rule{routeID: routeID, predicate: predicate}, nil
}

func (r Rule) RouteID() string           { return r.routeID }
func (r Rule) Matches(ctx *Context) bool { return r.predicate(ctx) }

// Strategy 路由策略。
type Strategy interface {
	Select(ctx *Context, routes []Route) (Decision, error)
}

// Router 路由器：路由表 + 策略。Prompt 入口先做显式标注裁决，再落到策略。
type Router struct {
	routes     []Route
	routeIndex map[string]Route
	strategy   Strategy
}

// NewRouter 校验并构建（路由非空、无 nil 路由、id 不重复、策略非 nil）。
func NewRouter(routes []Route, strategy Strategy) (*Router, error) {
	if len(routes) == 0 {
		return nil, errors.New("router: the routes are required")
	}
	if strategy == nil {
		return nil, errors.New("router: the routing strategy is required")
	}
	routeIndex := make(map[string]Route, len(routes))
	list := make([]Route, 0, len(routes))
	for _, route := range routes {
		// 零值路由判定：经 NewRoute 构建的路由必有模型，chatModel 为 nil 即零值/未初始化
		if route.chatModel == nil {
			return nil, errors.New("router: the route is required")
		}
		if _, dup := routeIndex[route.ID()]; dup {
			return nil, fmt.Errorf("router: duplicate route id: %s", route.ID())
		}
		routeIndex[route.ID()] = route
		list = append(list, route)
	}
	return &Router{routes: list, routeIndex: routeIndex, strategy: strategy}, nil
}

// Routes 路由表（顺序保持构建序）。
func (r *Router) Routes() []Route { return append([]Route(nil), r.routes...) }

// Prompt 路由入口：显式标注 → 策略选择 → 校验路由 id → 目标模型出请求描述。
// 入参形态与 ChatModel.Prompt 一致（string | *Message | []*Message | *Prompt）。
func (r *Router) Prompt(p any) (ai.RequestDesc, error) {
	prompt, err := promptOf(p)
	if err != nil {
		return nil, err
	}

	decision, err := r.resolveDecision(prompt)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(decision.RouteID) == "" {
		return nil, errors.New("router: the routing decision routeId is required")
	}
	route, ok := r.routeIndex[decision.RouteID]
	if !ok {
		return nil, fmt.Errorf("router: unknown route id: %s", decision.RouteID)
	}
	return route.chatModel.Prompt(prompt), nil
}

// resolveDecision 显式标注优先：ATTR_ROUTE_ID 存在且为非空字符串时直接定案，
// 不咨询策略（对标 Java resolveDecision）。
func (r *Router) resolveDecision(prompt *ai.Prompt) (Decision, error) {
	if v, ok := prompt.Attrs()[AttrRouteID]; ok && v != nil {
		s, isStr := v.(string)
		if !isStr || strings.TrimSpace(s) == "" {
			return Decision{}, errors.New("router: the explicit route id must be a non-empty string")
		}
		return Decision{RouteID: s, Reasoning: "explicit route"}, nil
	}
	ctx, err := NewContext(prompt)
	if err != nil {
		return Decision{}, err
	}
	return r.strategy.Select(ctx, r.routes)
}

// promptOf 入参形态归一（ChatModel.promptOf 的 error 版）。
func promptOf(p any) (*ai.Prompt, error) {
	switch v := p.(type) {
	case nil:
		return nil, errors.New("router: the prompt is required")
	case string:
		return ai.PromptOf(v), nil
	case *ai.Prompt:
		return v, nil
	case *ai.Message:
		return ai.NewPrompt(v), nil
	case []*ai.Message:
		return ai.NewPrompt(v...), nil
	}
	return nil, fmt.Errorf("router: unsupported prompt type %T (string | *ai.Message | []*ai.Message | *ai.Prompt)", p)
}
