package aisdk

import (
	"crypto/rand"
	"encoding/hex"
)

// IDGenerator part 标识生成策略（对标 AiSdkIdGenerator：prefix + 12 位随机十六进制）。
type IDGenerator func(prefix string) string

// DefaultIDGenerator 缺省策略（prefix + 随机 12 hex，与 Java DEFAULT 形态一致）。
func DefaultIDGenerator(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// 常用前缀快捷（对标 ofMessage/ofText/ofReasoning/ofToolCall/ofSource）。
func idsOfMessage(g IDGenerator) string   { return g("msg_") }
func idsOfText(g IDGenerator) string      { return g("txt_") }
func idsOfReasoning(g IDGenerator) string { return g("rsn_") }
func idsOfToolCall(g IDGenerator) string  { return g("call_") }
func idsOfSource(g IDGenerator) string    { return g("src_") }
