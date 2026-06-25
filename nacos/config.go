package nacos

import (
	"fmt"

	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

// Config holds Nacos connection and service registration settings.
//
// A single Config covers three concerns:
//   - connection: ServerAddr, Namespace, Username, Password
//   - config center: Group, DataID
//   - service registry/discovery: ServiceName, ServiceIP, ServicePort
type Config struct {
	// Enabled gates whether the Plugin actually connects to Nacos. When false,
	// Start is a no-op, so callers can keep the plugin registered and toggle
	// it from configuration.
	Enabled bool `json:"enabled"`

	ServerAddr string `json:"server_addr"` // host:port of the Nacos server
	Namespace  string `json:"namespace"`   // tenant/namespace id (empty = public)
	Group      string `json:"group"`       // config & service group
	DataID     string `json:"data_id"`     // config data id to watch (empty disables config listen)

	// Service registration. ServiceIP auto-detected when empty; ServicePort
	// defaults to 8080 when zero.
	ServiceName string `json:"service_name"`
	ServiceIP   string `json:"service_ip"`
	ServicePort uint64 `json:"service_port"`

	// Username/Password carry credentials for Nacos 2.x auth (optional).
	Username string `json:"username"`
	Password string `json:"password"`
}

// listenParam returns the config watch parameters derived from cfg.
func listenParam(cfg *Config) vo.ConfigParam {
	return vo.ConfigParam{DataId: cfg.DataID, Group: cfg.Group}
}

// GetConfig fetches a configuration value from Nacos. Empty dataID/group fall
// back to the Plugin's configured values.
func (p *Plugin) GetConfig(dataID, group string) (string, error) {
	if dataID == "" {
		dataID = p.cfg.DataID
	}
	if group == "" {
		group = p.cfg.Group
	}
	content, err := p.config.GetConfig(vo.ConfigParam{DataId: dataID, Group: group})
	if err != nil {
		return "", fmt.Errorf("nacos get config: %w", err)
	}
	return content, nil
}

// PublishConfig writes a configuration value to Nacos.
func (p *Plugin) PublishConfig(dataID, group, content string) error {
	if dataID == "" {
		dataID = p.cfg.DataID
	}
	if group == "" {
		group = p.cfg.Group
	}
	ok, err := p.config.PublishConfig(vo.ConfigParam{DataId: dataID, Group: group, Content: content})
	if err != nil {
		return fmt.Errorf("nacos publish config: %w", err)
	}
	if !ok {
		return fmt.Errorf("nacos publish config failed")
	}
	return nil
}

// startConfigListen delivers the current config to the callback once (so
// callers don't wait for a change) and then registers for server-side push
// notifications via the SDK.
func (p *Plugin) startConfigListen() error {
	dataID := p.cfg.DataID
	group := p.cfg.Group

	// Deliver the initial content so callers don't have to wait for a change.
	if content, err := p.GetConfig(dataID, group); err != nil {
		p.logger.Warn("nacos fetch initial config: %v", err)
	} else if content != "" && p.ConfigChangeCallback != nil {
		p.ConfigChangeCallback(dataID, group, content)
	}

	return p.config.ListenConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  group,
		OnChange: func(namespace, group, dataId, data string) {
			p.logger.Info("nacos config changed: %s/%s", group, dataId)
			if p.ConfigChangeCallback != nil {
				p.ConfigChangeCallback(dataId, group, data)
			}
		},
	})
}

// FetchConfig fetches a configuration value without a Plugin instance, e.g.
// during application bootstrap before the Plugin has been constructed.
func FetchConfig(cfg *Config) (string, error) {
	cli, err := configClientFor(cfg)
	if err != nil {
		return "", fmt.Errorf("nacos get config: %w", err)
	}
	content, err := cli.GetConfig(vo.ConfigParam{DataId: cfg.DataID, Group: cfg.Group})
	if err != nil {
		return "", fmt.Errorf("nacos get config: %w", err)
	}
	return content, nil
}
