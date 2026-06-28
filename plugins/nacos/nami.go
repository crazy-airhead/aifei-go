package nacos

import (
	"fmt"
	"sync"

	"github.com/crazy-airhead/aifei-go/nami"

	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

var (
	defaultCfg   *Config
	defaultCfgMu sync.RWMutex
)

// SetDefaultConfig sets the global Nacos config used by NewNamiUpstream(name).
// Called automatically by NewPlugin, or call it manually if not using the Plugin.
func SetDefaultConfig(cfg *Config) {
	defaultCfgMu.Lock()
	defer defaultCfgMu.Unlock()
	defaultCfg = cfg
}

// DefaultConfig returns the global default Nacos config (may be nil).
func DefaultConfig() *Config {
	defaultCfgMu.RLock()
	defer defaultCfgMu.RUnlock()
	return defaultCfg
}

// NewNamiUpstream creates a nami.Upstream using the global default Nacos config.
// Only the service name is required — serverAddr, namespace, and group are read
// from the default config.
//
// The returned upstream lazily resolves the config on first call and
// re-resolves whenever the global config changes. This makes it safe to create
// at package init time, before SetDefaultConfig has been called.
//
//	upstream := nacos.NewNamiUpstream("user-service")
//	n := nami.NewBuilder().Upstream(upstream).Name("user-service").Build()
func NewNamiUpstream(name string) nami.Upstream {
	var (
		mu           sync.Mutex
		realUpstream nami.Upstream
		lastCfg      *Config
	)
	return func() string {
		cfg := DefaultConfig()

		mu.Lock()
		if cfg != lastCfg {
			if cfg != nil {
				realUpstream = discoveryUpstream(cfg, name)
			} else {
				realUpstream = nil
			}
			lastCfg = cfg
		}
		mu.Unlock()

		if realUpstream == nil {
			return ""
		}
		return realUpstream()
	}
}

// NewNamiUpstreamWith creates a nami.Upstream with explicit settings.
// Use this when you don't have a global config or need to override it.
func NewNamiUpstreamWith(serverAddr, namespace, group, name string) nami.Upstream {
	return discoveryUpstream(&Config{
		ServerAddr: serverAddr,
		Namespace:  namespace,
		Group:      group,
	}, name)
}

// NewNamiUpstream creates a nami.Upstream backed by this Plugin's Nacos config.
// Only the service name is required.
func (p *Plugin) NewNamiUpstream(name string) nami.Upstream {
	return discoveryUpstream(p.cfg, name)
}

// discoveryUpstream builds a nami.Upstream backed by Nacos service discovery.
// If the underlying SDK client cannot be created (e.g. the server is
// unreachable), it returns an upstream that always resolves to empty — the
// caller's RPC then fails fast instead of panicking.
func discoveryUpstream(cfg *Config, name string) nami.Upstream {
	nc, err := namingClientFor(cfg)
	if err != nil {
		return func() string { return "" }
	}
	return nami.NewDiscoveryUpstream(&nacosDiscovery{naming: nc}, cfg.Group, name)
}

type nacosDiscovery struct {
	naming naming_client.INamingClient
}

// GetServer resolves a healthy instance URL for the service.
func (d *nacosDiscovery) GetServer(group, name string) (string, error) {
	instances, err := d.naming.SelectInstances(vo.SelectInstancesParam{
		ServiceName: name,
		GroupName:   group,
		HealthyOnly: true,
	})
	if err != nil {
		return "", fmt.Errorf("nacos discovery: %w", err)
	}
	if len(instances) == 0 {
		return "", fmt.Errorf("nacos discovery: no healthy instances for %s@%s", group, name)
	}
	inst := instances[0]
	return fmt.Sprintf("http://%s:%d", inst.Ip, inst.Port), nil
}
