// parent_proxy.go 父代理解析器（对标 ParentProxyResolver，Java 版自身
// 是 parent-proxy.ts 的忠实移植）。从显式配置或环境变量（HTTP_PROXY/
// HTTPS_PROXY/NO_PROXY 及小写形态）解析上游代理；NO_PROXY 匹配带 CIDR
// 与主机名后缀两类（golang httpproxy 语义）。
package sandbox

import (
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/log"
)

var (
	// parentSchemePattern scheme 前缀（schemeless host:port 形态按 curl
	// 补 http://）。
	parentSchemePattern = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
	parentDigitsOnly    = regexp.MustCompile(`^\d+$`)
	// parentBracketedWithPort [v6] 或 [v6]:port 形态。
	parentBracketedWithPort = regexp.MustCompile(`^\[([^\]]+)\](?::\d+)?$`)
	parentUserinfoPattern   = regexp.MustCompile(`//[^@/]*@`)
)

// 恒直连的回环段：127/8 整段、::1 精确、v4 映射回环段 ::ffff:127/8。
var parentLoopbackCIDRs = func() []*parentCidrEntry {
	var list []*parentCidrEntry
	for _, c := range []struct {
		ip     string
		prefix int
	}{{"127.0.0.0", 8}, {"::1", 128}, {"::ffff:127.0.0.0", 104}} {
		if e := newParentCidrEntry(c.ip, c.prefix); e != nil {
			list = append(list, e)
		}
	}
	return list
}()

// ParentProxyResolver 父代理解析结果：HTTP/HTTPS 代理 URL + NO_PROXY
// 规则。零值不可用——经 NewParentProxyResolver 构造。
type ParentProxyResolver struct {
	httpProxyURL  string
	httpsProxyURL string
	noProxyRules  parentNoProxyRules
}

// NewParentProxyResolver 解析父代理（对标构造器 resolveParentProxy）：
// HTTP 代理 = config.http → HTTP_PROXY → http_proxy；HTTPS 代理 =
// config.https → HTTPS_PROXY → https_proxy → 回落 HTTP 值（curl 同款）；
// NO_PROXY = config.noProxy → NO_PROXY → no_proxy → 空。环境变量以
// LookupEnv 判存在（设了空串即视为已设置并终止链——Java null 链同款）。
func NewParentProxyResolver(cfg *ParentProxyConfig) *ParentProxyResolver {
	var httpRaw, httpsRaw, noProxyRaw string
	if cfg != nil {
		httpRaw, httpsRaw, noProxyRaw = cfg.HTTP, cfg.HTTPS, cfg.NoProxy
	}
	if !strSet(httpRaw) {
		httpRaw = envFirstSet("HTTP_PROXY", "http_proxy")
	}
	if !strSet(httpsRaw) {
		httpsRaw = envFirstSet("HTTPS_PROXY", "https_proxy")
	}
	if !strSet(httpsRaw) {
		// 回落 HTTP 代理值（curl 行为）
		httpsRaw = httpRaw
	}
	if !strSet(noProxyRaw) {
		noProxyRaw = envFirstSet("NO_PROXY", "no_proxy")
	}

	return &ParentProxyResolver{
		httpProxyURL:  parseParentProxyURL(httpRaw),
		httpsProxyURL: parseParentProxyURL(httpsRaw),
		noProxyRules:  parseParentNoProxy(noProxyRaw),
	}
}

// HTTPProxyURL 已解析的 HTTP 代理 URL（空 = 未配置）。
func (r *ParentProxyResolver) HTTPProxyURL() string { return r.httpProxyURL }

// HTTPSProxyURL 已解析的 HTTPS 代理 URL（空 = 未配置）。
func (r *ParentProxyResolver) HTTPSProxyURL() string { return r.httpsProxyURL }

// SelectParentProxyURL 按目标是否 HTTPS 选父代理：HTTPS 目标 https 值
// 优先、回落 http 值；明文 HTTP 目标只用 http 值不回落 https（curl 语义
// ——仅配 HTTPS_PROXY 时 HTTP 请求直连）。
func (r *ParentProxyResolver) SelectParentProxyURL(isHTTPS bool) string {
	if isHTTPS {
		if r.httpsProxyURL != "" {
			return r.httpsProxyURL
		}
		return r.httpProxyURL
	}
	return r.httpProxyURL
}

// ShouldBypassProxy 目标是否绕过父代理直连：localhost 恒直连；回环段
// （127/8、::1、::ffff:127/8）恒直连；NO_PROXY 规则按 CIDR/后缀匹配。
// port 参数仅为 API 兼容保留——端口级 NO_PROXY 条目在解析期已剥端口
// 后缀（Java 同款，不参与判定）。
func (r *ParentProxyResolver) ShouldBypassProxy(host string, port int) bool {
	_ = port
	// 规范化：小写、剥尾点、剥 IPv6 方括号
	h := StripBrackets(strings.ToLower(host))
	h = strings.TrimSuffix(h, ".")

	if h == "localhost" {
		return true
	}

	isIP := isIPAddress(h)
	if isIP && matchesAnyParentCidr(h, parentLoopbackCIDRs) {
		return true
	}

	if r.noProxyRules.all {
		return true
	}

	if isIP && len(r.noProxyRules.cidrs) > 0 {
		if matchesAnyParentCidr(h, r.noProxyRules.cidrs) {
			return true
		}
	}

	for _, v := range r.noProxyRules.suffixes {
		if strings.HasPrefix(v, ".") {
			// .example.com 同时匹配 foo.example.com 与裸域 example.com
			if h == v[1:] || strings.HasSuffix(h, v) {
				return true
			}
		} else {
			// example.com 匹配裸域与 foo.example.com
			if h == v || strings.HasSuffix(h, "."+v) {
				return true
			}
		}
	}
	return false
}

// ---- URL 解析 ----

// parseParentProxyURL 解析并校验代理 URL（空 = 无效忽略）：接受
// schemeless host:port（curl 同款补 http://），拒绝非 http/https scheme。
func parseParentProxyURL(raw string) string {
	if raw == "" {
		return ""
	}
	withScheme := raw
	if !parentSchemePattern.MatchString(raw) {
		withScheme = "http://" + raw
	}
	u, err := url.Parse(withScheme)
	if err != nil || u.Scheme == "" {
		log.Debug("sandbox: invalid parent proxy URL, ignoring: %s", redactUserinfo(raw))
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		log.Debug("sandbox: invalid parent proxy URL, ignoring (unsupported scheme): %s", redactUserinfo(raw))
		return ""
	}
	if u.Hostname() == "" {
		log.Debug("sandbox: invalid parent proxy URL, ignoring (empty host): %s", redactUserinfo(raw))
		return ""
	}
	return u.String()
}

// ---- NO_PROXY 解析 ----

type parentNoProxyRules struct {
	all      bool
	cidrs    []*parentCidrEntry
	suffixes []string
}

// parseParentNoProxy 解析 NO_PROXY（对标 parseNoProxy）：
//   - "*" → 全部直连；
//   - 含 "/" → CIDR 条目（IP 合法 + 前缀纯数字且在族上限内）；畸形
//     CIDR 整条忽略（"/" 不是合法主机名字符，不当后缀用）；
//   - "*.example.com" 归一为 ".example.com"；裸 IP 字面量转 /32 或
//     /128 精确 CIDR；非 IP 条目剥尾 ":port"（仅纯数字段）后作后缀。
func parseParentNoProxy(raw string) parentNoProxyRules {
	var rules parentNoProxyRules
	if raw == "" {
		return rules
	}

	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		if entry == "*" {
			rules.all = true
			continue
		}

		if slash := strings.Index(entry, "/"); slash != -1 {
			ip, prefixStr := entry[:slash], entry[slash+1:]
			if isIPAddress(ip) && prefixStr != "" && parentDigitsOnly.MatchString(prefixStr) {
				if prefix, err := strconv.Atoi(prefixStr); err == nil {
					if addr := net.ParseIP(StripBrackets(ip)); addr != nil {
						// 家族按字面量判（点分十进制 = /32 上限）：当前 Go 的
						// ParseIP 对点分十进制也回 16 字节，按长度判会恒取 128
						maxPrefix := 32
						if strings.Contains(StripBrackets(ip), ":") {
							maxPrefix = 128
						}
						if prefix >= 0 && prefix <= maxPrefix {
							if cidr := newParentCidrEntry(StripBrackets(ip), prefix); cidr != nil {
								rules.cidrs = append(rules.cidrs, cidr)
							}
							continue
						}
					}
				}
			}
			// 畸形 CIDR 整条忽略
			continue
		}

		v := strings.ToLower(entry)
		if m := parentBracketedWithPort.FindStringSubmatch(v); m != nil {
			v = m[1]
		}
		if strings.HasPrefix(v, "*.") {
			v = v[1:] // → ".example.com"
		}

		if isIPAddress(v) {
			// 裸 IP 字面量 → /32 或 /128 精确匹配；构造失败回落后缀。
			// 前缀家族按字面量判（同上——ParseIP 长度不可作家族依据）
			if addr := net.ParseIP(StripBrackets(v)); addr != nil {
				prefixLen := 32
				if strings.Contains(StripBrackets(v), ":") {
					prefixLen = 128
				}
				if cidr := newParentCidrEntry(StripBrackets(v), prefixLen); cidr != nil {
					rules.cidrs = append(rules.cidrs, cidr)
					continue
				}
			}
		}

		if !isIPAddress(v) {
			if colon := strings.LastIndex(v, ":"); colon != -1 {
				if parentDigitsOnly.MatchString(v[colon+1:]) {
					v = v[:colon]
				}
			}
		}
		rules.suffixes = append(rules.suffixes, v)
	}
	return rules
}

// ---- CIDR 匹配 ----

// parentCidrEntry CIDR 网段（网络字节 + 前缀长度），字节级匹配
// IPv4/IPv6（长度不等即不匹配——v4 与 v6 条目互不相干，Java 同款）。
type parentCidrEntry struct {
	network      []byte
	prefixLength int
}

func newParentCidrEntry(ip string, prefixLength int) *parentCidrEntry {
	addr := net.ParseIP(ip)
	if addr == nil {
		return nil
	}
	return &parentCidrEntry{network: parentAddrBytes(ip, addr), prefixLength: prefixLength}
}

// parentAddrBytes 按字面量家族规范化地址字节（netip 家族保真的手工等价
// 物）：点分十进制 → 4 字节；v6 字面量（含 v4 映射形）→ 16 字节。当前
// Go 的 net.ParseIP 对点分十进制也返回 16 字节 v4 映射形——若直接采用，
// 127.0.0.0/8 的前 8 位（全零）会匹配一切 v4 主机，故须按字面量显式收窄
// （Java InetAddress/Go netip 都按字面量保真家族）。
func parentAddrBytes(literal string, addr net.IP) []byte {
	if !strings.Contains(literal, ":") {
		if v4 := addr.To4(); v4 != nil {
			return append([]byte(nil), v4...)
		}
	}
	return append([]byte(nil), addr.To16()...)
}

func (e *parentCidrEntry) matches(hostBytes []byte) bool {
	if len(hostBytes) != len(e.network) {
		return false
	}
	fullBytes := e.prefixLength / 8
	remainingBits := e.prefixLength % 8

	for i := 0; i < fullBytes; i++ {
		if e.network[i] != hostBytes[i] {
			return false
		}
	}
	if remainingBits > 0 && fullBytes < len(e.network) {
		mask := byte(0xFF << (8 - remainingBits))
		if e.network[fullBytes]&mask != hostBytes[fullBytes]&mask {
			return false
		}
	}
	return true
}

func matchesAnyParentCidr(host string, cidrs []*parentCidrEntry) bool {
	bare := StripBrackets(host)
	ip := net.ParseIP(bare)
	if ip == nil {
		return false
	}
	b := parentAddrBytes(bare, ip)
	for _, c := range cidrs {
		if c.matches(b) {
			return true
		}
	}
	return false
}

// ---- 工具 ----

// redactUserinfo 日志脱敏：//user:pass@ → //***:***@（尽力而为）。
func redactUserinfo(raw string) string {
	return parentUserinfoPattern.ReplaceAllString(raw, "//***:***@")
}

// strSet 非空已设置判定（Java null-or-empty 链）。
func strSet(s string) bool { return s != "" }

// envFirstSet 首个已设置（哪怕空串）的环境变量值；都未设置返回 "" 且
// 不可区分——调用方仅在「配置缺省」时走此链，未设置即整体空。
func envFirstSet(keys ...string) string {
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			return v
		}
	}
	return ""
}

// envAnySet 任一环境变量已设置（Java initialize 的父代理环境探测门）。
func envAnySet(keys ...string) bool {
	for _, k := range keys {
		if _, ok := os.LookupEnv(k); ok {
			return true
		}
	}
	return false
}

// resolveParentProxy Manager 装配用的父代理解析（对标 Java initialize）：
// 显式配置优先；否则环境变量存在（HTTP_PROXY/http_proxy/HTTPS_PROXY/
// https_proxy 任一设置）即走环境解析；都无则 nil。
func resolveParentProxy(cfg *RuntimeConfig) *ParentProxyResolver {
	if cfg != nil && cfg.Network != nil && cfg.Network.ParentProxy != nil {
		return NewParentProxyResolver(cfg.Network.ParentProxy)
	}
	if envAnySet("HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy") {
		return NewParentProxyResolver(nil)
	}
	return nil
}
