package httpx

import (
	"bufio"
	"context"
	"io"
	"strings"
)

// maxSSELineLen SSE 单行上限（1MiB：base64 媒体帧会很大）。
const maxSSELineLen = 1024 * 1024

// ParseSSE SSE 帧解析（对标 TextStreamUtil.parseSseStream）：按行状态机。
//   - data: 多行以 \n 拼接；event: 归一化小写；字段值剥离冒号后的一个可选空格
//   - 注释行（: 开头）忽略；空行分帧；结尾无空行的尾帧也交付
//   - [DONE] 作为普通 data 交付（哨兵由方言识别）
//   - 读循环逐行检查 ctx.Done()（取消即刻生效）
//
// onEvent 返回 error 立即中止并透传。
func ParseSSE(ctx context.Context, r io.Reader, onEvent func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxSSELineLen)

	var event string
	var data []string
	flush := func() error {
		if event == "" && len(data) == 0 {
			return nil
		}
		if err := onEvent(event, strings.Join(data, "\n")); err != nil {
			return err
		}
		event, data = "", nil
		return nil
	}

	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSuffix(sc.Text(), "\r")
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// 注释行（心跳保活等）忽略
		case strings.HasPrefix(line, "event:"):
			event = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "event:")))
		case strings.HasPrefix(line, "data:"):
			data = append(data, stripOneSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return flush()
}

// stripOneSpace SSE 字段值规范：冒号后至多一个前导空格属于分隔符。
func stripOneSpace(s string) string {
	if strings.HasPrefix(s, " ") {
		return s[1:]
	}
	return s
}
