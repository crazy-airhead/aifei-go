package enjoy_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// ISSUE-0011 验收：Scope.Set 自内向外查找改写、赋值支持索引键、Field 取值支持 getter。

// --- 1. Set 向上查找：#for 循环体内 #set 改写外层变量 ---

// 对照 ISSUE-0011 期望行为 1：Scope.Set 自内向外查找已存在变量并就地改写。
// 旧实现只写当前层，循环内 #set 无法改写外层 x，渲染后 x 仍为 0；修复后 x = 0+1+2+3 = 6。
func TestIssue0011SetWalksUpFromForBody(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-set-walkup")
	tpl := engine.GetTemplateByString(`#set(x = 0)#for(i : [1,2,3])#set(x = x + i)#end#(x)`)

	got := renderToString(t, tpl, map[string]interface{}{})
	if got != "6" {
		t.Fatalf("循环内 #set 应改写外层 x 至 6，got %q", got)
	}
}

// 嵌套循环：内层 #set 改写的是「最近一层已声明该变量的作用域」。这里 sum 声明在最外层，
// 内层循环体 #set(sum = sum + ...) 仍改写最外层 sum。
func TestIssue0011SetWalksUpNestedLoop(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-set-nested")
	tpl := engine.GetTemplateByString(`#set(sum = 0)#for(i : [1,2])#for(j : [10,20])#set(sum = sum + i * j)#end#end#(sum)`)

	got := renderToString(t, tpl, map[string]interface{}{})
	// 1*10 + 1*20 + 2*10 + 2*20 = 10+20+20+40 = 90
	if got != "90" {
		t.Fatalf("嵌套循环内 #set 应改写最外层 sum 至 90，got %q", got)
	}
}

// 回归：循环变量本身仍是局部（SetLocal），不外泄到外层。循环结束后外层读不到 i。
func TestIssue0011LoopVarStaysLocal(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-loop-local")
	tpl := engine.GetTemplateByString(`#for(i : [1,2,3])#end[#(i)]`)

	got := renderToString(t, tpl, map[string]interface{}{})
	// 外层无 i，#(i) 取空
	if got != "[]" {
		t.Fatalf("循环变量 i 应局部、外层取空，期望 '[]'，got %q", got)
	}
}

// --- 2. 索引赋值：map[key]=v / list[i]=v / 连写 ---

// 对照 ISSUE-0011 期望行为 2：赋值支持索引键 map[key]=v。
func TestIssue0011MapIndexAssign(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-map-assign")
	tpl := engine.GetTemplateByString(`#set(m['k'] = 'v')#(m['k'])`)

	got := renderToString(t, tpl, map[string]interface{}{
		"m": map[string]interface{}{},
	})
	if got != "v" {
		t.Fatalf("期望 map['k']='v'，got %q", got)
	}
}

// 已有 key 的 map 索引赋值改写原值。
func TestIssue0011MapIndexOverwrite(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-map-overwrite")
	tpl := engine.GetTemplateByString(`#set(m['k'] = 'new')#(m['k'])`)

	got := renderToString(t, tpl, map[string]interface{}{
		"m": map[string]interface{}{"k": "old"},
	})
	if got != "new" {
		t.Fatalf("期望覆盖为 'new'，got %q", got)
	}
}

// list[i]=v 索引赋值。
func TestIssue0011ListIndexAssign(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-list-assign")
	tpl := engine.GetTemplateByString(`#set(arr[2] = 99)#(arr[0]),#(arr[1]),#(arr[2])`)

	got := renderToString(t, tpl, map[string]interface{}{
		"arr": []interface{}{1, 2, 3},
	})
	if got != "1,2,99" {
		t.Fatalf("期望 arr[2]=99 → '1,2,99'，got %q", got)
	}
}

// 无限连写：a = m['k'] = 7，三者都得 7（右结合）。
func TestIssue0011ChainedAssign(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-chain")
	tpl := engine.GetTemplateByString(`#set(a = m['k'] = 7)#(a)-#(m['k'])`)

	got := renderToString(t, tpl, map[string]interface{}{
		"m": map[string]interface{}{},
	})
	if got != "7-7" {
		t.Fatalf("期望连写 a 与 m['k'] 均为 7 → '7-7'，got %q", got)
	}
}

// 索引内含赋值（对照 Java 注释 id = array[i = 0] = array[1] = 123 的形态）：
// #set(arr[i = 0] = 11) 既写入 arr[0]=11，又把 i 置 0。
func TestIssue0011AssignInsideIndex(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-idx-assign")
	tpl := engine.GetTemplateByString(`#set(arr[i = 0] = 11)#(i):#(arr[0])`)

	got := renderToString(t, tpl, map[string]interface{}{
		"arr": []interface{}{0, 0, 0},
	})
	if got != "0:11" {
		t.Fatalf("期望 i=0 且 arr[0]=11 → '0:11'，got %q", got)
	}
}

// 表达式内的赋值也成立：#(...) 里 a=5 输出 5 并写入 a。
func TestIssue0011AssignInOutputExpr(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-expr-assign")
	tpl := engine.GetTemplateByString(`#(a = 5)-#(a)`)

	got := renderToString(t, tpl, map[string]interface{}{})
	if got != "5-5" {
		t.Fatalf("期望表达式内赋值 a=5 → '5-5'，got %q", got)
	}
}

// --- 3. Field 取值支持 getter ---

// getterPOJO 模拟 Java 风格 POJO：私有字段 + getXxx() getter。
type getterPOJO struct {
	name   string
	active bool
}

func (p getterPOJO) GetName() string  { return p.name }
func (p *getterPOJO) GetActive() bool { return p.active }

// 对照 ISSUE-0011 期望行为 3：Field 取值优先调 getXxx()。name 为私有字段，只能经 getter 取到。
func TestIssue0011FieldGetterValueReceiver(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-getter-val")
	tpl := engine.GetTemplateByString(`#(user.name)`)

	got := renderToString(t, tpl, map[string]interface{}{
		"user": getterPOJO{name: "james", active: true},
	})
	if got != "james" {
		t.Fatalf("期望经 GetName() 取到 'james'，got %q", got)
	}
}

// 指针接收者 getter：传入指针，active 经 GetActive() 取到。
func TestIssue0011FieldGetterPointerReceiver(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-getter-ptr")
	tpl := engine.GetTemplateByString(`#(user.active)`)

	got := renderToString(t, tpl, map[string]interface{}{
		"user": &getterPOJO{name: "james", active: true},
	})
	if got != "true" {
		t.Fatalf("期望经 GetActive() 取到 'true'，got %q", got)
	}
}

// getterShadow 同时具备导出字段 Name 与同名 getter GetName()：getter 优先（对照 Java Field 优先级）。
type getterShadow struct {
	Name string
}

func (s getterShadow) GetName() string { return "FROM-GETTER" }

func TestIssue0011FieldGetterPriorityOverField(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-getter-priority")
	tpl := engine.GetTemplateByString(`#(user.Name)`)

	got := renderToString(t, tpl, map[string]interface{}{
		"user": getterShadow{Name: "field-value"},
	})
	if got != "FROM-GETTER" {
		t.Fatalf("期望 getter 优先于同名字段 → 'FROM-GETTER'，got %q", got)
	}
}

// 无 getter 时回退到导出字段（对照 Java public field）。
type plainField struct {
	Age int
}

func TestIssue0011FieldFallsBackToStructField(t *testing.T) {
	engine := enjoy.NewEngine("issue0011-field-fallback")
	tpl := engine.GetTemplateByString(`#(user.Age)`)

	got := renderToString(t, tpl, map[string]interface{}{
		"user": plainField{Age: 18},
	})
	if got != "18" {
		t.Fatalf("期望回退到导出字段 Age=18，got %q", got)
	}
}
