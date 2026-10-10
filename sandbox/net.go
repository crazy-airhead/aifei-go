// net.go 网络过滤面（对标 net 包三件）：主机校验/规范化（无 DNS 解析）、
// 域名模式匹配、请求过滤（fail-closed：无配置/畸形主机/回调异常一律拒绝）。
package sandbox

import (
	"net/url"
	"strings"

	"github.com/crazy-airhead/aifei-go/log"
)

// isIPAddress 是否 IPv4/IPv6 字面量（不做 DNS 解析；对标 isIPAddress）。
func isIPAddress(host string) bool {
	if host == "" {
		return false
	}
	bare := StripBrackets(host)
	return isValidIPv4(bare) || isValidIPv6Literal(bare)
}

func isValidIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 3 {
			return false
		}
		// 拒绝八进制（前导零）与十六进制形态
		if len(part) > 1 && part[0] == '0' {
			return false
		}
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
		if n := atoi(part); n < 0 || n > 255 {
			return false
		}
	}
	return true
}

func isValidIPv6Literal(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == ':') {
			return false
		}
	}
	// 至少两个冒号才与主机名可区分
	return strings.Count(s, ":") >= 2
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// StripBrackets 剥离 IPv6 方括号。
func StripBrackets(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

// IsValidHost 主机名校验：拒绝控制字符与 %；IP 字面量或 DNS 标签集
// （字母数字._-）。对标 isValidHost。
func IsValidHost(h string) bool {
	if h == "" || len(h) > 255 {
		return false
	}
	bare := StripBrackets(h)
	if strings.Contains(bare, "%") {
		return false
	}
	if isIPAddress(bare) {
		return true
	}
	if bare == "" {
		return false
	}
	for i := 0; i < len(bare); i++ {
		c := bare[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

// CanonicalizeHost 主机规范化（无 DNS 解析）：拒绝控制字符与 %、
// IPv4 仅收标准四段形态（inet_aton 简写/八进制/十六进制拒绝）、
// IPv6 压缩形态原样、主机名去尾点。失败返回 ""。对标 canonicalizeHost。
func CanonicalizeHost(h string) string {
	if h == "" {
		return ""
	}
	bare := StripBrackets(h)

	// 拒绝控制字符与 %
	for i := 0; i < len(bare); i++ {
		c := bare[i]
		if c <= 0x1f || c == 0x7f || c == '%' {
			return ""
		}
	}

	if isValidIPv4(bare) {
		return bare
	}

	// 拒绝非标准 IPv4 形态（纯数字加点但非四段）
	if strings.ContainsAny(bare, ".") && isDigitsAndDots(bare) {
		return ""
	}

	if strings.Contains(bare, ":") {
		if !isValidIPv6Literal(bare) {
			return ""
		}
		return bare // 压缩形态即规范形态
	}

	// 主机名：URL 解析取规范形态
	if u, err := url.Parse("http://" + bare + "/"); err == nil && u.Hostname() != "" {
		result := StripBrackets(u.Hostname())
		return strings.TrimSuffix(result, ".")
	}
	return ""
}

func isDigitsAndDots(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c == '.') {
			return false
		}
	}
	return true
}

// MatchesDomainPattern 域名模式匹配（对标 matchesDomainPattern）：
// *.example.com 匹配子域不匹配裸域；通配永不匹配 IP 字面量；
// 非通配模式大小写不敏感全等。
func MatchesDomainPattern(hostname, pattern string) bool {
	h := strings.ToLower(hostname)

	if strings.HasPrefix(pattern, "*.") {
		if isIPAddress(StripBrackets(h)) {
			return false
		}
		baseDomain := strings.ToLower(pattern[2:])
		return strings.HasSuffix(h, "."+baseDomain)
	}

	return h == strings.ToLower(pattern)
}

// FilterNetworkRequest 网络请求过滤（对标 NetworkFilter.filter，逐分支：
// 无配置→拒；畸形主机→拒；黑名单先判；白名单放行；无命中走交互裁决，
// 无回调或回调出错→拒）。fail-closed。
func FilterNetworkRequest(port int, host string, cfg *RuntimeConfig, callback AskCallback) bool {
	if cfg == nil {
		log.Debug("sandbox: no config available, denying network request")
		return false
	}

	if !IsValidHost(host) {
		log.Error("sandbox: denying malformed host %s:%d", host, port)
		return false
	}

	canonicalHost := CanonicalizeHost(host)
	if canonicalHost == "" {
		canonicalHost = host
	}

	if cfg.Network == nil {
		return false
	}

	// 黑名单先判
	for _, deniedDomain := range cfg.Network.DeniedDomains {
		if MatchesDomainPattern(canonicalHost, deniedDomain) {
			log.Debug("sandbox: denied by config rule %s:%d", host, port)
			return false
		}
	}

	// 白名单放行
	for _, allowedDomain := range cfg.Network.AllowedDomains {
		if MatchesDomainPattern(canonicalHost, allowedDomain) {
			log.Debug("sandbox: allowed by config rule %s:%d", host, port)
			return true
		}
	}

	// 无命中：交互裁决；无回调 fail-closed
	if callback == nil {
		log.Debug("sandbox: no matching config rule, denying %s:%d", host, port)
		return false
	}

	allowed, err := callback(HostPattern{Host: host, Port: port})
	if err != nil {
		log.Error("sandbox: error in permission callback: %v", err)
		return false
	}
	return allowed
}
