package ai_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/ai"
)

// —— NewTool 链式构建 → InputSchema 生成 ——

func TestToolInputSchema(t *testing.T) {
	tool := ai.NewTool("query-order").
		Desc("查询订单").
		StringParam("orderId", "订单号", true).
		IntParam("topK", "返回条数", false).
		FloatParam("minAmount", "最小金额", false).
		BoolParam("paid", "是否支付", false).
		DateTimeParam("since", "起始时间", false).
		Build()
	got := tool.InputSchema()
	want := `{"properties":{` +
		`"minAmount":{"description":"最小金额","type":"number"},` +
		`"orderId":{"description":"订单号","type":"string"},` +
		`"paid":{"description":"是否支付","type":"boolean"},` +
		`"since":{"description":"起始时间","format":"date-time","type":"string"},` +
		`"topK":{"description":"返回条数","type":"integer"}},` +
		`"required":["orderId"],"type":"object"}`
	if got != want {
		t.Fatalf("InputSchema =\n%s\nwant\n%s", got, want)
	}
}

func TestToolInputSchemaParamExtras(t *testing.T) {
	tool := ai.NewTool("ship-order").
		Desc("发货").
		Param(ai.ParamSpec{Name: "color", Type: "string", Desc: "颜色", Enum: []any{"red", "green"}}).
		Param(ai.ParamSpec{Name: "ids", Type: "array", Desc: "编号", Required: true,
			Items: map[string]any{"type": "integer"}}).
		Param(ai.ParamSpec{Name: "copies", Type: "integer", Desc: "份数", Default: 3}).
		Build()
	got := tool.InputSchema()
	want := `{"properties":{` +
		`"color":{"description":"颜色","enum":["red","green"],"type":"string"},` +
		`"copies":{"default":3,"description":"份数","type":"integer"},` +
		`"ids":{"description":"编号","items":{"type":"integer"},"type":"array"}},` +
		`"required":["ids"],"type":"object"}`
	if got != want {
		t.Fatalf("InputSchema =\n%s\nwant\n%s", got, want)
	}
}

func TestToolInputSchemaJSONOverride(t *testing.T) {
	tool := ai.NewTool("raw").
		StringParam("ignored", "忽略", true).
		InputSchemaJSON(`{"type":"object","properties":{"x":{"type":"integer"}}}`).
		Build()
	if got := tool.InputSchema(); got != `{"type":"object","properties":{"x":{"type":"integer"}}}` {
		t.Fatalf("InputSchema = %s", got)
	}
}

func TestToolFlags(t *testing.T) {
	tool := ai.NewTool("t").
		Title("标题").
		Strict(true).
		ReturnDirect(true).
		MetaSet("k", "v").
		Build()
	if tool.Title() != "标题" {
		t.Fatalf("Title = %q", tool.Title())
	}
	if tool.Strict() == nil || !*tool.Strict() {
		t.Fatalf("Strict = %v", tool.Strict())
	}
	if !tool.ReturnDirect() {
		t.Fatal("ReturnDirect = false")
	}
	if tool.Meta()["k"] != "v" {
		t.Fatalf("Meta = %v", tool.Meta())
	}
}

// —— Call：成功 / error / panic → recover ——

func TestToolCall(t *testing.T) {
	echo := ai.NewTool("echo").
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return "hi " + args["name"].(string), nil
		}).Build()
	res := echo.Call(context.Background(), map[string]any{"name": "a"})
	if res.IsError || res.ContentText() != "hi a" {
		t.Fatalf("res = %+v", res)
	}

	boom := ai.NewTool("boom").
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return nil, errors.New("db down")
		}).Build()
	res = boom.Call(context.Background(), nil)
	if !res.IsError || res.ContentText() != "Error: db down" {
		t.Fatalf("res = %+v", res)
	}

	kaboom := ai.NewTool("kaboom").
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			panic("explode")
		}).Build()
	res = kaboom.Call(context.Background(), nil)
	if !res.IsError || res.ContentText() != "Error: panic: explode" {
		t.Fatalf("res = %+v", res)
	}
}

// resultConvert：string 直出、非 string JSON 序列化、nil → 空。
func TestToolCallResultConvert(t *testing.T) {
	jsonTool := ai.NewTool("j").
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return map[string]any{"a": 1}, nil
		}).Build()
	if res := jsonTool.Call(context.Background(), nil); res.ContentText() != `{"a":1}` {
		t.Fatalf("res = %q", res.ContentText())
	}

	nilTool := ai.NewTool("n").
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return nil, nil
		}).Build()
	if res := nilTool.Call(context.Background(), nil); res.ContentText() != "" || res.IsError {
		t.Fatalf("res = %+v", res)
	}

	noHandler := ai.NewTool("h").Build()
	if res := noHandler.Call(context.Background(), nil); !res.IsError {
		t.Fatalf("res = %+v", res)
	}
}

// —— RegisterTools 反射绑定（_OrderTools 样例，Java 测试同名类搬译）——

type shipAddress struct {
	City string `param:"city,required" desc:"城市"`
	Zip  string `param:"zip" desc:"邮编"`
}

type orderQueryArgs struct {
	OrderID string      `param:"orderId,required" desc:"订单号"`
	TopK    int         `param:"topK" desc:"返回条数" default:"5"`
	Since   time.Time   `param:"since" desc:"起始时间"`
	Tags    []string    `param:"tags" desc:"标签"`
	Addr    shipAddress `param:"addr" desc:"地址"`
}

type orderSummary struct {
	Total int `json:"total" desc:"总数"`
}

type orderTools struct{ ctxSeen bool }

func (o *orderTools) QueryOrder(ctx context.Context, a orderQueryArgs) (string, error) {
	o.ctxSeen = ctx != nil
	if a.OrderID == "" {
		return "", errors.New("missing orderId")
	}
	return "order-" + a.OrderID, nil
}

func (o *orderTools) CheckStock(a orderQueryArgs) (int, error) { return a.TopK, nil }

func (o *orderTools) CancelOrder(ctx context.Context, a orderQueryArgs) error {
	return errors.New("boom")
}

func (o *orderTools) PanicOrder(ctx context.Context, a orderQueryArgs) (string, error) {
	panic("kaboom")
}

func (o *orderTools) Ping() (string, error) { return "pong", nil }

func (o *orderTools) Summarize(a orderQueryArgs) (orderSummary, error) {
	return orderSummary{Total: 3}, nil
}

// NotASig 参数非 (ctx, struct) 签名 → 跳过。
func (o *orderTools) NotASig(a, b string) (string, error) { return "", nil }

// helper 未导出 → 跳过。
func (o *orderTools) helper(a orderQueryArgs) (string, error) { return "", nil }

func TestRegisterTools(t *testing.T) {
	ot := &orderTools{}
	provider := ai.RegisterTools(ot)

	var names []string
	byName := map[string]ai.FunctionTool{}
	for _, tl := range provider.Tools() {
		names = append(names, tl.Name())
		byName[tl.Name()] = tl
	}
	// 方法名字母序 + kebab 化；不匹配签名/未导出方法跳过
	wantNames := []string{"cancel-order", "check-stock", "panic-order", "ping", "query-order", "summarize"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("names = %v, want %v", names, wantNames)
	}

	// 嵌套 struct 展开 / time.Time → date-time / default 按类型转换 / required 保声明序
	got := byName["query-order"].InputSchema()
	want := `{"properties":{` +
		`"addr":{"description":"地址","properties":{"city":{"description":"城市","type":"string"},"zip":{"description":"邮编","type":"string"}},"required":["city"],"type":"object"},` +
		`"orderId":{"description":"订单号","type":"string"},` +
		`"since":{"description":"起始时间","format":"date-time","type":"string"},` +
		`"tags":{"description":"标签","items":{"type":"string"},"type":"array"},` +
		`"topK":{"default":5,"description":"返回条数","type":"integer"}},` +
		`"required":["orderId"],"type":"object"}`
	if got != want {
		t.Fatalf("query-order schema =\n%s\nwant\n%s", got, want)
	}

	// 无参方法 schema 为空 object
	if got := byName["ping"].InputSchema(); got != `{"properties":{},"required":[],"type":"object"}` {
		t.Fatalf("ping schema = %s", got)
	}

	// ReturnType / 输出 schema：基础类型忽略、struct 生成
	if byName["check-stock"].ReturnType().Kind() != reflect.Int {
		t.Fatalf("check-stock ReturnType = %v", byName["check-stock"].ReturnType())
	}
	if byName["check-stock"].OutputSchema() != "" {
		t.Fatalf("check-stock OutputSchema = %q", byName["check-stock"].OutputSchema())
	}
	wantOut := `{"properties":{"total":{"description":"总数","type":"integer"}},"required":[],"type":"object"}`
	if got := byName["summarize"].OutputSchema(); got != wantOut {
		t.Fatalf("summarize OutputSchema =\n%s\nwant\n%s", got, wantOut)
	}

	// Call：参数 JSON 往返绑定 + ctx 透传
	res := byName["query-order"].Call(context.Background(), map[string]any{"orderId": "A1", "topK": 3})
	if res.IsError || res.ContentText() != "order-A1" {
		t.Fatalf("res = %+v", res)
	}
	if !ot.ctxSeen {
		t.Fatal("ctx 未透传到方法")
	}

	// error 返回 → "Error: " 前缀；panic → recover
	if res := byName["query-order"].Call(context.Background(), map[string]any{"topK": 3}); !res.IsError ||
		res.ContentText() != "Error: missing orderId" {
		t.Fatalf("res = %+v", res)
	}
	if res := byName["cancel-order"].Call(context.Background(), map[string]any{"orderId": "x"}); !res.IsError ||
		res.ContentText() != "Error: boom" {
		t.Fatalf("res = %+v", res)
	}
	if res := byName["panic-order"].Call(context.Background(), map[string]any{"orderId": "x"}); !res.IsError ||
		res.ContentText() != "Error: panic: kaboom" {
		t.Fatalf("res = %+v", res)
	}

	// int 返回 → JSON 序列化；无参方法直呼
	if res := byName["check-stock"].Call(context.Background(), map[string]any{"orderId": "x", "topK": 7}); res.IsError ||
		res.ContentText() != "7" {
		t.Fatalf("res = %+v", res)
	}
	if res := byName["ping"].Call(context.Background(), nil); res.IsError || res.ContentText() != "pong" {
		t.Fatalf("res = %+v", res)
	}
	if res := byName["summarize"].Call(context.Background(), map[string]any{"orderId": "x"}); res.IsError ||
		res.ContentText() != `{"total":3}` {
		t.Fatalf("res = %+v", res)
	}
}

// ToolNamer 显式命名/描述优先于 kebab 化默认名。
type namedTools struct{}

func (n *namedTools) ToolName(method string) (name, desc string, ok bool) {
	if method == "DoIt" {
		return "custom-name", "自定义描述", true
	}
	return "", "", false
}

func (n *namedTools) DoIt(a orderQueryArgs) (string, error) { return "ok", nil }

func TestRegisterToolsNamer(t *testing.T) {
	provider := ai.RegisterTools(&namedTools{})
	tools := provider.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	if tools[0].Name() != "custom-name" || tools[0].Description() != "自定义描述" {
		t.Fatalf("tool = %q / %q", tools[0].Name(), tools[0].Description())
	}
}

// —— OutputSchemaOf ——

func TestOutputSchemaOf(t *testing.T) {
	if got := ai.OutputSchemaOf[string](); got != `{"type":"string"}` {
		t.Fatalf("string schema = %s", got)
	}
	want := `{"properties":{"total":{"description":"总数","type":"integer"}},"required":[],"type":"object"}`
	if got := ai.OutputSchemaOf[orderSummary](); got != want {
		t.Fatalf("struct schema =\n%s\nwant\n%s", got, want)
	}
	if got := ai.OutputSchemaOf[time.Time](); got != `{"format":"date-time","type":"string"}` {
		t.Fatalf("time schema = %s", got)
	}
}
