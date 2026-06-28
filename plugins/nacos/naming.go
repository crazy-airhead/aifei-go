package nacos

import (
	"fmt"
	"net"

	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

func (p *Plugin) instanceIP() string {
	if p.cfg.ServiceIP != "" {
		return p.cfg.ServiceIP
	}
	return getLocalIP()
}

func (p *Plugin) instancePort() uint64 {
	if p.cfg.ServicePort != 0 {
		return p.cfg.ServicePort
	}
	return 8080
}

// registerInstance registers this service with Nacos as an ephemeral instance;
// the SDK maintains heartbeats internally — no manual beat loop is needed.
func (p *Plugin) registerInstance() error {
	ip := p.instanceIP()
	port := p.instancePort()

	ok, err := p.naming.RegisterInstance(vo.RegisterInstanceParam{
		Ip:          ip,
		Port:        port,
		ServiceName: p.cfg.ServiceName,
		GroupName:   p.cfg.Group,
		Weight:      1.0,
		Enable:      true,
		Healthy:     true,
		Ephemeral:   true,
		ClusterName: "DEFAULT",
	})
	if err != nil {
		return fmt.Errorf("nacos register: %w", err)
	}
	if !ok {
		return fmt.Errorf("nacos register failed")
	}
	p.logger.Info("nacos instance registered: %s:%d@%s", ip, port, p.cfg.ServiceName)
	return nil
}

// deregisterInstance removes this service from Nacos.
func (p *Plugin) deregisterInstance() error {
	ip := p.instanceIP()
	port := p.instancePort()

	ok, err := p.naming.DeregisterInstance(vo.DeregisterInstanceParam{
		Ip:          ip,
		Port:        port,
		ServiceName: p.cfg.ServiceName,
		GroupName:   p.cfg.Group,
		Ephemeral:   true,
	})
	if err != nil {
		return fmt.Errorf("nacos deregister: %w", err)
	}
	if !ok {
		return fmt.Errorf("nacos deregister failed")
	}
	p.logger.Info("nacos instance deregistered: %s:%d@%s", ip, port, p.cfg.ServiceName)
	return nil
}

// getLocalIP returns the first non-loopback IPv4 address, falling back to
// 127.0.0.1 when none is found.
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return "127.0.0.1"
}
