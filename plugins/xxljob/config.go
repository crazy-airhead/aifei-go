package xxljob

import (
	"fmt"
	"time"

	"github.com/crazy-airhead/aifei-go/config"
)

// Config holds the xxl-job executor configuration read from the global config
// store under the given prefix (default "xxljob").
type Config struct {
	ServerAddr   string        `yaml:"serverAddr"`   // scheduling center URL
	AccessToken  string        `yaml:"accessToken"`  // API token
	Timeout      time.Duration `yaml:"timeout"`      // HTTP timeout for scheduler calls
	ExecutorIp   string        `yaml:"executorIp"`   // local executor IP (auto-detected if empty)
	ExecutorPort string        `yaml:"executorPort"` // local executor port
	RegistryKey  string        `yaml:"registryKey"`  // executor name
	LogDir       string        `yaml:"logDir"`       // log directory
}

// LoadConfig reads xxl-job configuration from the global config under prefix
// (empty defaults to "xxljob").
func LoadConfig(prefix string) (*Config, error) {
	if prefix == "" {
		prefix = "xxljob"
	}
	cfg := &Config{
		ServerAddr:   config.GetStr(prefix + ".serverAddr"),
		AccessToken:  config.GetStr(prefix + ".accessToken"),
		Timeout:      time.Duration(config.GetInt(prefix+".timeoutMs")) * time.Millisecond,
		ExecutorIp:   config.GetStr(prefix + ".executorIp"),
		ExecutorPort: config.GetStr(prefix + ".executorPort"),
		RegistryKey:  config.GetStr(prefix + ".registryKey"),
		LogDir:       config.GetStr(prefix + ".logDir"),
	}

	// Also support timeout as a duration string
	if timeoutStr := config.GetStr(prefix + ".timeout"); timeoutStr != "" {
		d, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("xxljob: invalid timeout %q: %w", timeoutStr, err)
		}
		cfg.Timeout = d
	}

	return cfg, nil
}

// toOptions converts Config to functional Options.
func (c *Config) toOptions() []Option {
	var opts []Option
	if c.ServerAddr != "" {
		opts = append(opts, ServerAddr(c.ServerAddr))
	}
	if c.AccessToken != "" {
		opts = append(opts, AccessToken(c.AccessToken))
	}
	if c.ExecutorIp != "" {
		opts = append(opts, ExecutorIp(c.ExecutorIp))
	}
	if c.ExecutorPort != "" {
		opts = append(opts, ExecutorPort(c.ExecutorPort))
	}
	if c.RegistryKey != "" {
		opts = append(opts, RegistryKey(c.RegistryKey))
	}
	return opts
}
