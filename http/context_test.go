package http

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func newCtx(method, target, contentType, body string) *HttpContext {
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	r := httptest.NewRequest(method, target, rd)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return NewInput(r)
}

func TestGetStrsQueryRepeatedKeys(t *testing.T) {
	c := newCtx("GET", "/x?ids=a&ids=b", "", "")
	if got := c.GetStrs("ids"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("GetStrs = %v", got)
	}
}

func TestGetIntsQueryRepeatedKeys(t *testing.T) {
	c := newCtx("GET", "/x?ids=1&ids=2&ids=3", "", "")
	if got := c.GetInts("ids"); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestGetIntsQuerySkipsUnparsable(t *testing.T) {
	c := newCtx("GET", "/x?ids=1&ids=x&ids=3", "", "")
	if got := c.GetInts("ids"); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestGetIntsFormRepeatedKeys(t *testing.T) {
	c := newCtx("POST", "/x", "application/x-www-form-urlencoded", "ids=1&ids=2")
	if got := c.GetInts("ids"); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestGetIntsJSONBody(t *testing.T) {
	c := newCtx("POST", "/x", "application/json", `{"ids":[1,2,3]}`)
	if got := c.GetInts("ids"); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestGetIntsJSONBodyMixedElements(t *testing.T) {
	// 数字字符串可解析，非数字字符串与浮点小数跳过
	c := newCtx("POST", "/x", "application/json", `{"ids":[1,"2","x",3.5,4]}`)
	if got := c.GetInts("ids"); !reflect.DeepEqual(got, []int{1, 2, 4}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestGetStrsJSONBody(t *testing.T) {
	c := newCtx("POST", "/x", "application/json", `{"names":["a","b"],"nums":[1,2]}`)
	if got := c.GetStrs("names"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("GetStrs(names) = %v", got)
	}
	if got := c.GetStrs("nums"); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatalf("GetStrs(nums) = %v", got)
	}
}

func TestArrayGettersQueryWinsOverForm(t *testing.T) {
	// 与 getVal 一致：query 优先
	c := newCtx("POST", "/x?ids=9", "application/x-www-form-urlencoded", "ids=1")
	if got := c.GetInts("ids"); !reflect.DeepEqual(got, []int{9}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestArrayGettersDefaults(t *testing.T) {
	c := newCtx("GET", "/x", "", "")
	if got := c.GetInts("ids"); got != nil {
		t.Fatalf("GetInts no-default = %v, want nil", got)
	}
	if got := c.GetInts("ids", []int{7}); !reflect.DeepEqual(got, []int{7}) {
		t.Fatalf("GetInts default = %v", got)
	}
	if got := c.GetStrs("ids"); got != nil {
		t.Fatalf("GetStrs no-default = %v, want nil", got)
	}
	if got := c.GetStrs("ids", []string{"d"}); !reflect.DeepEqual(got, []string{"d"}) {
		t.Fatalf("GetStrs default = %v", got)
	}
}

func TestArrayGettersJSONScalarNotArray(t *testing.T) {
	// body[key] 非数组时不取值（不报错），落到默认
	c := newCtx("POST", "/x", "application/json", `{"ids":1}`)
	if got := c.GetInts("ids", []int{5}); !reflect.DeepEqual(got, []int{5}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestGetStrsQueryCommaSeparated(t *testing.T) {
	c := newCtx("GET", "/x?ids=a,b,c", "", "")
	if got := c.GetStrs("ids"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("GetStrs = %v", got)
	}
}

func TestGetIntsQueryCommaSeparated(t *testing.T) {
	c := newCtx("GET", "/x?ids=1,2,3", "", "")
	if got := c.GetInts("ids"); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestArrayGettersCommaMixedWithRepeatedKeys(t *testing.T) {
	// 逗号分割与重复 key 可混用
	c := newCtx("GET", "/x?ids=1,2&ids=3", "", "")
	if got := c.GetInts("ids"); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestArrayGettersCommaTrimsAndDropsEmptySegments(t *testing.T) {
	// "+" 在 query 中解码为空格：分段 trim；空段（含尾逗号）丢弃
	c := newCtx("GET", "/x?ids=1,+2&ids=3,", "", "")
	if got := c.GetStrs("ids"); !reflect.DeepEqual(got, []string{"1", "2", "3"}) {
		t.Fatalf("GetStrs = %v", got)
	}
}

func TestArrayGettersEmptyValueReadsAsMissing(t *testing.T) {
	// "ids=" 全空 → 视为缺失，走默认（与 GetStr 的空值即缺省一致）
	c := newCtx("GET", "/x?ids=", "", "")
	if got := c.GetStrs("ids"); got != nil {
		t.Fatalf("GetStrs no-default = %v, want nil", got)
	}
	if got := c.GetStrs("ids", []string{"d"}); !reflect.DeepEqual(got, []string{"d"}) {
		t.Fatalf("GetStrs default = %v", got)
	}
	if got := c.GetInts("ids", []int{9}); !reflect.DeepEqual(got, []int{9}) {
		t.Fatalf("GetInts default = %v", got)
	}
}

func TestGetIntsFormCommaSeparated(t *testing.T) {
	c := newCtx("POST", "/x", "application/x-www-form-urlencoded", "ids=1,2")
	if got := c.GetInts("ids"); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("GetInts = %v", got)
	}
}

func TestGetStrsJSONStringElementSplit(t *testing.T) {
	// 统一分割同样作用于 JSON 数组里的字符串元素
	c := newCtx("POST", "/x", "application/json", `{"ids":["1,2","3"]}`)
	if got := c.GetStrs("ids"); !reflect.DeepEqual(got, []string{"1", "2", "3"}) {
		t.Fatalf("GetStrs = %v", got)
	}
}
