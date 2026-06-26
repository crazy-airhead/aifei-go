package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CloudLoader is a callback for L5 cloud configuration (e.g., Nacos).
// It receives the current store (after L1-L4 are applied) and returns
// YAML content to deep-merge as the final layer. Return nil bytes to skip.
type CloudLoader func(store *Props) ([]byte, error)

// Option configures the Init/Load pipeline.
type Option func(*loaderConfig)

// loaderConfig holds the pipeline configuration.
type loaderConfig struct {
	envPrefix string   // env var prefix, default "AIFEI"
	env       string   // forced env, empty means auto-detect
	configDir string   // base directory for config files, default "."
	baseFiles []string // base config file names, default ["app.yml"]
}

// defaultLoaderConfig returns the default pipeline configuration.
func defaultLoaderConfig() *loaderConfig {
	return &loaderConfig{
		envPrefix: "AIFEI",
		configDir: ".",
		baseFiles: []string{"app.yml"},
	}
}

// WithEnvPrefix sets the environment variable prefix for L2 and L3.
// Default is "AIFEI". Env vars matching <prefix>_* are loaded into the props.
// The prefix also determines the profile env var name (<prefix>_ENV, <prefix>_PROFILE)
// and the config-include env var name (<prefix>_CONFIG_INCLUDE).
func WithEnvPrefix(prefix string) Option {
	return func(c *loaderConfig) {
		c.envPrefix = prefix
	}
}

// WithEnv forces a specific environment profile (e.g., "dev", "prod").
// When set, this value is used instead of checking --env or env vars.
func WithEnv(env string) Option {
	return func(c *loaderConfig) {
		c.env = env
	}
}

// WithConfigDir sets the base directory for config file lookups.
// Default is the current working directory.
func WithConfigDir(dir string) Option {
	return func(c *loaderConfig) {
		c.configDir = dir
	}
}

// WithBaseFiles sets the base config file names to load at L1.
// Default is ["app.yml"]. The env-specific variant is derived automatically
// (e.g., "app-dev.yml").
func WithBaseFiles(files ...string) Option {
	return func(c *loaderConfig) {
		if len(files) > 0 {
			c.baseFiles = files
		}
	}
}

// cloudLoaders holds registered cloud config loaders.
var cloudLoaders []CloudLoader

// RegisterCloudLoader registers a cloud config loader for L5.
// Packages like nacos can register their loader via init().
//
// Example:
//
//	func init() {
//	    config.RegisterCloudLoader(func(store *config.Store) ([]byte, error) {
//	        // fetch config from Nacos and return as YAML bytes
//	        return nacosClient.GetConfig()
//	    })
//	}
func RegisterCloudLoader(cl CloudLoader) {
	cloudLoaders = append(cloudLoaders, cl)
}

// Init runs the full configuration loading pipeline (L1-L5) and sets the global
// Props so package-level functions (Get, GetStr, etc.) work immediately.
// args should be os.Args (the full command-line arguments).
// args[0] is stored as "app.path".
//
// Loading order (later overrides earlier):
//
//	L1) base config files (default: app.yml), then env-specific variants
//	L2) extension configs from config.include + <prefix>_CONFIG_INCLUDE
//	L3) dynamic: <prefix>_ env vars + CLI args (--key=value)
//	L4) programmatic via Load()
//	L5) cloud config via registered CloudLoaders (e.g., Nacos)
func Init(args []string, opts ...Option) error {
	if err := Load(args, opts...); err != nil {
		return err
	}

	// L5: Apply cloud loaders
	for _, cl := range cloudLoaders {
		content, err := cl(globalProps)
		if err != nil {
			return fmt.Errorf("cloud loader: %w", err)
		}
		if len(content) > 0 {
			if err := globalProps.MergeYAML(content); err != nil {
				return fmt.Errorf("merge cloud config: %w", err)
			}
		}
	}

	return nil
}

// Load runs L1-L3 and sets the global Props.
// Useful when callers want to inspect or modify the props before
// applying L4 (Load) and L5 (cloud loaders via Init).
func Load(args []string, opts ...Option) error {
	cfg := defaultLoaderConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	store := NewProps()

	// Store args[0] as app.path
	if len(args) > 0 {
		store.Set("app.path", args[0])
	}

	// Separate --env flag from other args
	cliArgs := make([]string, 0, len(args))
	for _, arg := range args[1:] {
		trimmed := strings.TrimLeft(arg, "-")
		if strings.HasPrefix(trimmed, "env=") {
			continue // handled by resolveEnv
		}
		cliArgs = append(cliArgs, arg)
	}

	// Resolve environment
	env := cfg.env
	if env == "" {
		env = resolveEnv(args[1:], cfg)
	}

	// L1: Load base config files
	for _, base := range cfg.baseFiles {
		basePath := filepath.Join(cfg.configDir, base)
		if err := store.LoadYAML(basePath); err != nil {
			return err
		}

		// Load env-specific variant (e.g., app-dev.yml)
		if env != "" {
			envPath := filepath.Join(cfg.configDir, envFileName(base, env))
			if err := store.LoadYAML(envPath); err != nil {
				return err
			}
		}
	}

	// L2: Load extension configs
	extPaths := collectExtensionPaths(store, cfg)
	for _, p := range extPaths {
		fullPath := filepath.Join(cfg.configDir, p)
		if err := store.LoadYAMLPattern(fullPath); err != nil {
			return err
		}
	}

	// L3: Load dynamic overrides (env vars + CLI args)
	store.LoadEnv(cfg.envPrefix)
	store.LoadArgs(cliArgs)

	// Re-check env after L3 (e.g., --env=dev in CLI args may have been parsed)
	if env == "" {
		env = resolveEnv(cliArgs, cfg)
		if env != "" {
			for _, base := range cfg.baseFiles {
				envPath := filepath.Join(cfg.configDir, envFileName(base, env))
				_ = store.LoadYAML(envPath) // ignore error, file may not exist
			}
		}
	}

	// Set global Props so package-level functions work.
	SetProps(store)

	return nil
}

// LoadFiles loads YAML files into the global Props (L4 programmatic loading).
// Missing files are silently skipped. No-op if the global is nil.
func LoadFiles(paths ...string) error {
	if globalProps == nil {
		return nil
	}
	for _, p := range paths {
		if err := globalProps.LoadYAML(p); err != nil {
			return err
		}
	}
	return nil
}

// resolveEnv determines the active environment profile.
// Checks in order: --env flag, <prefix>_ENV, <prefix>_PROFILE.
func resolveEnv(args []string, cfg *loaderConfig) string {
	// Check --env and -env in args
	for _, arg := range args {
		trimmed := strings.TrimLeft(arg, "-")
		if strings.HasPrefix(trimmed, "env=") {
			val := strings.TrimPrefix(trimmed, "env=")
			if val != "" {
				return val
			}
		}
	}

	// Check env vars
	if env := os.Getenv(cfg.envPrefix + "_ENV"); env != "" {
		return env
	}
	if env := os.Getenv(cfg.envPrefix + "_PROFILE"); env != "" {
		return env
	}

	return ""
}

// envFileName derives the environment-specific config file name.
// "app.yml" + "dev" -> "app-dev.yml"
// "app.yaml" + "prod" -> "app-prod.yaml"
func envFileName(base, env string) string {
	ext := filepath.Ext(base)
	if ext == ".yml" || ext == ".yaml" {
		baseName := strings.TrimSuffix(base, ext)
		return baseName + "-" + env + ext
	}
	return base + "-" + env
}

// collectExtensionPaths gathers extension config paths from two sources:
// 1. The "config.include" key in the props (loaded from YAML)
// 2. The <prefix>_CONFIG_INCLUDE environment variable (comma-separated)
func collectExtensionPaths(store *Props, cfg *loaderConfig) []string {
	var paths []string

	// From store (config.include)
	if v := store.Get("config.include"); v != nil {
		switch list := v.(type) {
		case []interface{}:
			for _, item := range list {
				if s, ok := item.(string); ok {
					paths = append(paths, s)
				}
			}
		case []string:
			paths = append(paths, list...)
		}
	}

	// From env var
	envKey := cfg.envPrefix + "_CONFIG_INCLUDE"
	if envVal := os.Getenv(envKey); envVal != "" {
		for _, p := range splitComma(envVal) {
			paths = append(paths, p)
		}
	}

	return paths
}

// splitComma splits a comma-separated string, trimming whitespace
// and skipping empty entries.
func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
