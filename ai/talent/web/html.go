// html.go 零依赖 HTML 处理（尽力而为，非 flexmark 级保真——06 契约明示
// 延后）：HTMLToMarkdown 单遍标签扫描（标题/列表/引用/链接/强调/行内代码/
// 代码块/图片/表格横排/hr，实体解码，空白折叠）；ExtractTextFromHTML 去
// 不可见块后剥标签折叠空白（Jsoup text() 语义近似）。
package web

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// 不可见内容整块移除（script/style/noscript/iframe/object/embed）；
	// 标签名是常量，逐个预编译（RE2 无反向引用，写不了 \1 泛型形态）
	invisibleRes = compileInvisible("script", "style", "noscript", "iframe", "object", "embed")
	tagRe        = regexp.MustCompile(`<[^>]*>`)
	spaceRunRe   = regexp.MustCompile(`\s+`)
	nl3Re        = regexp.MustCompile(`\n{3,}`)
	numEntRe     = regexp.MustCompile(`&#(\d+);`)
	hexEntRe     = regexp.MustCompile(`&#x([0-9a-fA-F]+);`)
)

// compileInvisible 逐标签构造成对剔除正则。
func compileInvisible(names ...string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, 0, len(names))
	for _, n := range names {
		res = append(res, regexp.MustCompile(`(?is)<`+n+`\b[^>]*>.*?</`+n+`\s*>`))
	}
	return res
}

// stripInvisible 移除全部不可见块（script/style/…整对剔除）。
func stripInvisible(html string) string {
	for _, re := range invisibleRes {
		html = re.ReplaceAllString(html, "")
	}
	return html
}

// namedEntities 常用命名实体（覆盖常规正文足够；其余保留原文——尽力而为）。
var namedEntities = strings.NewReplacer(
	"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
	"&#39;", "'", "&apos;", "'", "&nbsp;", " ", "&mdash;", "—",
	"&hellip;", "…", "&copy;", "©",
)

// decodeEntities 实体解码（命名 + 十进制/十六进制数字）。
func decodeEntities(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	s = numEntRe.ReplaceAllStringFunc(s, func(m string) string {
		n, err := strconv.Atoi(m[2 : len(m)-1])
		if err != nil || n <= 0 || n > 0x10FFFF {
			return m
		}
		return string(rune(n))
	})
	s = hexEntRe.ReplaceAllStringFunc(s, func(m string) string {
		n, err := strconv.ParseUint(m[3:len(m)-1], 16, 32)
		if err != nil || n <= 0 || n > 0x10FFFF {
			return m
		}
		return string(rune(n))
	})
	return namedEntities.Replace(s)
}

// ExtractTextFromHTML 纯文本抽取：剥不可见块 → 剥全部标签 → 实体解码 →
// 空白折叠为单空格 → trim。
func ExtractTextFromHTML(html string) string {
	s := stripInvisible(html)
	s = tagRe.ReplaceAllString(s, "")
	s = decodeEntities(s)
	return strings.TrimSpace(spaceRunRe.ReplaceAllString(s, " "))
}

// HTMLToMarkdown 单遍扫描转换。策略：块级标签触发段落断行；行内格式标签
// 开/闭即时输出配对标记（嵌套自然成立）；a 靠最近开标签的 href 配对；li/h
// 的前缀延迟到块内首个文本；pre 进入原文模式。
func HTMLToMarkdown(html string) string {
	src := stripInvisible(html)
	var sb strings.Builder
	var line strings.Builder // 当前行（含前缀），段落断行时合并进 sb
	pendingPrefix := ""      // li/h 开标签挂起的前缀
	quoteDepth := 0
	inPre := false
	linkHref := ""

	flushLine := func() {
		if line.Len() > 0 {
			for i := 0; i < quoteDepth; i++ {
				sb.WriteString("> ")
			}
			sb.WriteString(line.String())
			line.Reset()
		}
	}
	paragraphBreak := func() {
		flushLine()
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
	}
	lineBreak := func() {
		flushLine()
		sb.WriteString("\n")
	}
	emit := func(s string) {
		if pendingPrefix != "" {
			line.WriteString(pendingPrefix)
			pendingPrefix = ""
		}
		line.WriteString(s)
	}

	i := 0
	for i < len(src) {
		c := src[i]
		if c != '<' {
			j := strings.IndexByte(src[i:], '<')
			var text string
			if j < 0 {
				text = src[i:]
				i = len(src)
			} else {
				text = src[i : i+j]
				i += j
			}
			if inPre {
				line.WriteString(text)
				continue
			}
			text = decodeEntities(text)
			// HTML 换行语义：内部空白折叠为单空格
			if t := strings.TrimSpace(spaceRunRe.ReplaceAllString(text, " ")); t != "" {
				emit(t)
			}
			continue
		}
		// 标签解析
		j := strings.IndexByte(src[i:], '>')
		if j < 0 { // 未闭合的孤立 '<'：当文本处理
			emit("<")
			i++
			continue
		}
		tag := src[i : i+j+1]
		i += j + 1
		name, attrs, closing := parseTag(tag)
		if name == "" {
			continue
		}
		if inPre {
			if closing && name == "pre" {
				inPre = false
				flushLine()
				sb.WriteString("\n```\n")
			}
			continue
		}
		switch {
		case name == "pre" && !closing:
			paragraphBreak()
			sb.WriteString("```")
			sb.WriteString("\n")
			line.Reset()
			inPre = true
		case !closing && (name == "h1" || name == "h2" || name == "h3" ||
			name == "h4" || name == "h5" || name == "h6"):
			paragraphBreak()
			pendingPrefix = strings.Repeat("#", int(name[1]-'0')) + " "
		case closing && strings.HasPrefix(name, "h") && len(name) == 2 && name[1] >= '1' && name[1] <= '6':
			lineBreak()
		case name == "li" && !closing:
			paragraphBreak()
			pendingPrefix = "- "
		case name == "br":
			lineBreak()
		case name == "hr":
			paragraphBreak()
			sb.WriteString("---")
			paragraphBreak()
		case name == "blockquote" && !closing:
			quoteDepth++
			paragraphBreak()
		case name == "blockquote" && closing:
			if quoteDepth > 0 {
				quoteDepth--
			}
			paragraphBreak()
		case name == "a" && !closing:
			linkHref = attrs["href"]
			if linkHref != "" {
				emit("[")
			}
		case name == "a" && closing:
			if linkHref != "" {
				emit("](" + linkHref + ")")
			}
			linkHref = ""
		case name == "img":
			alt := attrs["alt"]
			emit("![" + alt + "](" + attrs["src"] + ")")
		case name == "strong" || name == "b":
			emit("**")
		case name == "em" || name == "i":
			emit("*")
		case name == "code" && !inPre:
			emit("`")
		case name == "td" || name == "th":
			if closing {
				emit(" | ")
			}
		case name == "tr" && closing:
			lineBreak()
		case isBlockTag(name):
			paragraphBreak()
		}
	}
	paragraphBreak()
	out := sb.String()
	out = nl3Re.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

// isBlockTag 块级标签集合（段落断行处理）。
func isBlockTag(name string) bool {
	switch name {
	case "p", "div", "ul", "ol", "dl", "dt", "dd", "table", "thead", "tbody",
		"section", "article", "header", "footer", "nav", "main", "aside",
		"figure", "figcaption", "form", "fieldset", "address", "title", "li":
		return true
	}
	return false
}

// parseTag 解析标签名/闭合性/属性表（小写名；属性值容忍单双引号与裸值）。
func parseTag(tag string) (name string, attrs map[string]string, closing bool) {
	inner := strings.TrimSpace(tag[1 : len(tag)-1])
	inner = strings.TrimSuffix(inner, "/") // 自闭合斜杠
	if strings.HasPrefix(inner, "/") {
		closing = true
		inner = strings.TrimSpace(inner[1:])
	}
	if inner == "" {
		return "", nil, closing
	}
	parts := splitAttrs(inner)
	name = strings.ToLower(parts[0])
	attrs = map[string]string{}
	for _, kv := range parts[1:] {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[:eq]))
		v := strings.TrimSpace(kv[eq+1:])
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		attrs[k] = decodeEntities(v)
	}
	return name, attrs, closing
}

// splitAttrs 按空白切标签头与属性（引号内的空白不分隔）。
func splitAttrs(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := byte(0)
	for k := 0; k < len(s); k++ {
		ch := s[k]
		if inQuote != 0 {
			cur.WriteByte(ch)
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		switch ch {
		case '"', '\'':
			inQuote = ch
			cur.WriteByte(ch)
		case ' ', '\t', '\n', '\r':
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}
