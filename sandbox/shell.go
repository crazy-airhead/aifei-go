// shell.go POSIX shell 引用、命令 base64 摘要与代理环境变量注入
// （对标 ShellQuote / Base64Utils / ProxyEnvUtils）。
package sandbox

import (
	"encoding/base64"
	"os"
	"strconv"
	"strings"
)

// ShellQuote 参数数组转单行 POSIX shell 命令（逐参引用、空格连接）。
func ShellQuote(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = ShellQuoteArg(a)
	}
	return strings.Join(quoted, " ")
}

// ShellQuoteArg 单参数引用：仅安全字符免引；否则单引号包裹，
// 内嵌单引号转义为 '"'"'（Java quoteArg 同款）。
func ShellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if isSafeShellArg(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

// isSafeShellArg 安全字符集（字母数字与 _-./:,+=@%）。
func isSafeShellArg(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("_-./:,+=@%", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// encodeSandboxedCommand 命令截断 100 字符后 base64（logTag 载荷；
// 对标 encodeSandboxedCommand）。
func encodeSandboxedCommand(command string) string {
	truncated := command
	if len(truncated) > 100 {
		truncated = truncated[:100]
	}
	return base64.StdEncoding.EncodeToString([]byte(truncated))
}

// CATrustVars 代理信任 CA 证书的环境变量面（对标 CA_TRUST_VARS）。
var CATrustVars = []string{
	"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "CURL_CA_BUNDLE",
	"REQUESTS_CA_BUNDLE", "PIP_CERT", "GIT_SSL_CAINFO",
	"AWS_CA_BUNDLE", "CARGO_HTTP_CAINFO", "DENO_CERT",
}

// GenerateProxyEnvVars 生成沙箱内进程的代理与临时目录环境变量
// （KEY=VALUE 串，对标 generateProxyEnvVars）。
func GenerateProxyEnvVars(httpProxyPort, socksProxyPort *int, caCertPath string) []string {
	tmpdir := envOr("CLAUDE_CODE_TMPDIR", envOr("CLAUDE_TMPDIR", "/tmp/claude"))
	envVars := []string{
		"SANDBOX_RUNTIME=1",
		"TMPDIR=" + tmpdir,
	}

	if caCertPath != "" {
		for _, v := range CATrustVars {
			envVars = append(envVars, v+"="+caCertPath)
		}
	}

	if httpProxyPort == nil && socksProxyPort == nil {
		return envVars
	}

	noProxyAddresses := strings.Join([]string{
		"localhost", "127.0.0.1", "::1", "*.local", ".local",
		"169.254.0.0/16", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	}, ",")
	envVars = append(envVars, "NO_PROXY="+noProxyAddresses, "no_proxy="+noProxyAddresses)

	if httpProxyPort != nil {
		httpURL := "http://localhost:" + strconv.Itoa(*httpProxyPort)
		envVars = append(envVars,
			"HTTP_PROXY="+httpURL, "HTTPS_PROXY="+httpURL,
			"http_proxy="+httpURL, "https_proxy="+httpURL)
	}

	if socksProxyPort != nil {
		socksURL := "socks5h://localhost:" + strconv.Itoa(*socksProxyPort)
		envVars = append(envVars, "ALL_PROXY="+socksURL, "all_proxy="+socksURL)

		// SSH 走 SOCKS：macOS 用 nc -X 5，linux 用 socat PROXY
		sshMuxOverride := "-o ControlMaster=no -o ControlPath=none"
		switch DetectPlatform() {
		case MacOS:
			envVars = append(envVars, "GIT_SSH_COMMAND=ssh "+sshMuxOverride+
				" -o ProxyCommand='nc -X 5 -x localhost:"+strconv.Itoa(*socksProxyPort)+" %h %p'")
		case Linux:
			if httpProxyPort != nil {
				envVars = append(envVars, "GIT_SSH_COMMAND=ssh "+sshMuxOverride+
					" -o ProxyCommand='socat - PROXY:localhost:%h:%p,proxyport="+strconv.Itoa(*httpProxyPort)+"'")
			}
		}

		envVars = append(envVars,
			"FTP_PROXY="+socksURL, "ftp_proxy="+socksURL,
			"RSYNC_PROXY=localhost:"+strconv.Itoa(*socksProxyPort))

		dockerPort := socksProxyPort
		if httpProxyPort != nil {
			dockerPort = httpProxyPort
		}
		envVars = append(envVars,
			"DOCKER_HTTP_PROXY=http://localhost:"+strconv.Itoa(*dockerPort),
			"DOCKER_HTTPS_PROXY=http://localhost:"+strconv.Itoa(*dockerPort))

		if httpProxyPort != nil {
			envVars = append(envVars,
				"CLOUDSDK_PROXY_TYPE=http",
				"CLOUDSDK_PROXY_ADDRESS=localhost",
				"CLOUDSDK_PROXY_PORT="+strconv.Itoa(*httpProxyPort))
		}

		envVars = append(envVars, "GRPC_PROXY="+socksURL, "grpc_proxy="+socksURL)
	}

	return envVars
}

func envOr(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

// currentEnv 当前进程环境（spawn 透传用）。
func currentEnv() []string { return os.Environ() }
