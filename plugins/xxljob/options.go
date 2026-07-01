package xxljob

import (
	"time"

	"github.com/crazy-airhead/aifei-go/log"
	"github.com/go-basic/ipv4"
)

// Options holds the executor configuration.
type Options struct {
	ServerAddr   string        `json:"server_addr"`   // scheduling center URL
	AccessToken  string        `json:"access_token"`  // API token
	Timeout      time.Duration `json:"timeout"`       // HTTP timeout for scheduler calls
	ExecutorIp   string        `json:"executor_ip"`   // local executor IP (auto-detected if empty)
	ExecutorPort string        `json:"executor_port"` // local executor port
	RegistryKey  string        `json:"registry_key"`  // executor name
	LogDir       string        `json:"log_dir"`       // log directory

	logger log.Logger // logger
}

func newOptions(opts ...Option) Options {
	opt := Options{
		ExecutorIp:   ipv4.LocalIP(),
		ExecutorPort: DefaultExecutorPort,
		RegistryKey:  DefaultRegistryKey,
	}

	for _, o := range opts {
		o(&opt)
	}

	if opt.logger == nil {
		opt.logger = log.Default()
	}

	return opt
}

// Option is a functional option for configuring the executor.
type Option func(o *Options)

var (
	DefaultExecutorPort = "9999"
	DefaultRegistryKey  = "golang-jobs"
)

// ServerAddr sets the scheduling center URL.
func ServerAddr(addr string) Option {
	return func(o *Options) {
		o.ServerAddr = addr
	}
}

// AccessToken sets the API token.
func AccessToken(token string) Option {
	return func(o *Options) {
		o.AccessToken = token
	}
}

// ExecutorIp sets the executor IP.
func ExecutorIp(ip string) Option {
	return func(o *Options) {
		o.ExecutorIp = ip
	}
}

// ExecutorPort sets the executor port.
func ExecutorPort(port string) Option {
	return func(o *Options) {
		o.ExecutorPort = port
	}
}

// RegistryKey sets the executor identifier.
func RegistryKey(registryKey string) Option {
	return func(o *Options) {
		o.RegistryKey = registryKey
	}
}

// SetLogger sets the logger.
func SetLogger(l log.Logger) Option {
	return func(o *Options) {
		o.logger = l
	}
}
