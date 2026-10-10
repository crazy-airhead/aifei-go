// schema.go JSON-RPC 2.0 帧层（对标 McpSchema 的 JSONRPC* 家族与
// deserializeJsonRpcMessage 判别）：Request/Notification/Response/Error 与
// UnmarshalMessage 单一判别入口。
package mcp

import (
	"bytes"
	"fmt"

	"encoding/json"
)

// JSONRPCVersion 协议版本（恒 "2.0"，Java JSONRPC_VERSION）。
const JSONRPCVersion = "2.0"

// JSON-RPC 错误码（Java ErrorCodes）。
const (
	CodeParseError       = -32700
	CodeInvalidRequest   = -32600
	CodeMethodNotFound   = -32601
	CodeInvalidParams    = -32602
	CodeInternalError    = -32603
	CodeResourceNotFound = -32002
)

// Message 入站帧标记接口（Java JSONRPCMessage）。三种具体帧内嵌实现；
// 判别经类型断言（*JSONRPCRequest / *JSONRPCNotification / *JSONRPCResponse）。
type Message interface {
	isMessage()
}

// JSONRPCRequest 期望响应的请求。ID 为 string 或 int64（规范：非 float/null；
// UnmarshalMessage 校验拒绝畸形 id）。
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Meta    map[string]any  `json:"_meta,omitempty"` // progressToken 在此
}

func (*JSONRPCRequest) isMessage() {}

// JSONRPCNotification 通知（无 id、无响应）。
type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Meta    map[string]any  `json:"_meta,omitempty"`
}

func (*JSONRPCNotification) isMessage() {}

// JSONRPCResponse 响应：Result 与 Error 互斥（规范：二者必有其一）。
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"` // string | int64 | nil（nil 仅见于错误响应）
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

func (*JSONRPCResponse) isMessage() {}

// JSONRPCError JSON-RPC 错误对象。
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error 实现 error（Java McpError 携带 JSONRPCError 的对应物——会话层把
// 响应错误以本类型直接上抛，调用方 errors.As 取 Code/Data）。
func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("mcp: jsonrpc error %d: %s", e.Code, e.Message)
}

// UnmarshalMessage 判别反序列化（Java deserializeJsonRpcMessage 同款判别序）：
// method+id → Request；method 无 id → Notification；result|error → Response；
// 其余拒绝。Request 的 id 在此校验（string/int64——float/bool/null/容器畸形
// 即 InvalidRequest；Java convertValue 不校验，属 Go 侧契约加固）。Response
// 的 id 允许 nil（JSON-RPC 允许错误响应无 id；会话层丢弃并告警，Java 同款）。
func UnmarshalMessage(b []byte) (Message, error) {
	// 用 UseNumber 解码探针 map，避免大整数 id 经 float64 丢精度
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var probe map[string]any
	if err := dec.Decode(&probe); err != nil {
		return nil, &JSONRPCError{Code: CodeParseError, Message: "parse error: " + err.Error()}
	}
	if probe == nil {
		return nil, &JSONRPCError{Code: CodeParseError, Message: "parse error: null message"}
	}

	_, hasID := probe["id"]
	_, hasMethod := probe["method"]
	_, hasResult := probe["result"]
	_, hasError := probe["error"]

	switch {
	case hasMethod && hasID:
		id, err := NormalizeID(probe["id"])
		if err != nil {
			return nil, err
		}
		req := &JSONRPCRequest{JSONRPC: JSONRPCVersion, ID: id, Method: str(probe["method"])}
		if err := redecode(b, req); err != nil {
			return nil, err
		}
		req.ID = id
		return req, nil
	case hasMethod && !hasID:
		n := &JSONRPCNotification{JSONRPC: JSONRPCVersion, Method: str(probe["method"])}
		if err := redecode(b, n); err != nil {
			return nil, err
		}
		return n, nil
	case hasResult || hasError:
		resp := &JSONRPCResponse{JSONRPC: JSONRPCVersion}
		if err := redecode(b, resp); err != nil {
			return nil, err
		}
		// id 归一（string/int64 原样；null/缺失保持 nil，畸形拒绝）
		if raw, ok := probe["id"]; ok && raw != nil {
			id, err := NormalizeID(raw)
			if err != nil {
				return nil, err
			}
			resp.ID = id
		} else {
			resp.ID = nil
		}
		return resp, nil
	default:
		return nil, fmt.Errorf("mcp: cannot deserialize JSONRPCMessage: %s", string(b))
	}
}

// NormalizeID 校验并归一请求/响应 id：string、json.Number→int64（整型）合法；
// float 小数、null、bool、数组/对象畸形（InvalidRequest）。json.Number 形态
// 来自 UseNumber 探针；已归一的 int64/float64(整型) 也放行（内存构造的帧）。
func NormalizeID(v any) (any, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case int64:
		return t, nil
	case int:
		return int64(t), nil
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, nil
		}
		if f, err := t.Float64(); err == nil && f == float64(int64(f)) {
			return int64(f), nil
		}
	case float64:
		if t == float64(int64(t)) {
			return int64(t), nil
		}
	}
	return nil, &JSONRPCError{
		Code:    CodeInvalidRequest,
		Message: fmt.Sprintf("invalid request id: %v", v),
	}
}

// redecode 整帧重解码进目标结构（对齐 convertValue；Unknown 字段忽略）。
func redecode(b []byte, target any) error {
	if err := json.Unmarshal(b, target); err != nil {
		return &JSONRPCError{Code: CodeParseError, Message: "parse error: " + err.Error()}
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
