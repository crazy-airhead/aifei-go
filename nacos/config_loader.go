package nacos

import (
	"github.com/crazy-airhead/aifei-go/config"
)

func init() {
	config.RegisterCloudLoader(func(props *config.Props) ([]byte, error) {
		// Check if Nacos config loading is configured in the local config.
		serverAddr := props.GetStr("nacos.serverAddr")
		if serverAddr == "" {
			return nil, nil // not configured, skip
		}
		dataID := props.GetStr("nacos.dataId")
		if dataID == "" {
			return nil, nil // no data_id, skip
		}

		cfg := &Config{
			ServerAddr: serverAddr,
			Namespace:  props.GetStr("nacos.namespace"),
			Group:      props.GetStr("nacos.group"),
			DataID:     dataID,
			Username:   props.GetStr("nacos.username"),
			Password:   props.GetStr("nacos.password"),
		}

		content, err := FetchConfig(cfg)
		if err != nil {
			return nil, err
		}
		return []byte(content), nil
	})
}

// BindProps binds a config.Props to this Plugin for dynamic config updates.
// When Nacos pushes a config change via this plugin's watched DataID, the
// Props is automatically updated via YAML deep-merge.
//
// If a ConfigChangeCallback was already set, BindProps wraps it so both
// the Props update and the existing callback are invoked on each change.
//
// Example:
//
//	if err := config.Init(os.Args); err != nil { ... }
//	p := nacos.NewPlugin(cfg, nil)
//
// Returns the Plugin for method chaining.
func (p *Plugin) BindProps(props *config.Props) *Plugin {
	prev := p.ConfigChangeCallback
	p.ConfigChangeCallback = func(dataID, group, content string) {
		if content != "" {
			if err := props.LoadYAMLBytes([]byte(content)); err != nil {
				p.logger.Warn("nacos bindprops merge: %v", err)
			}
		}
		if prev != nil {
			prev(dataID, group, content)
		}
	}
	return p
}
