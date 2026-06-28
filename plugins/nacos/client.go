package nacos

import (
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

// clientKey uniquely identifies a shared SDK client connection: a different
// server, namespace, or username means a separate gRPC connection.
func clientKey(cfg *Config) string {
	return cfg.ServerAddr + "|" + cfg.Namespace + "|" + cfg.Username
}

// clientEntry holds lazily-created, process-wide SDK clients for one
// (server, namespace, user) tuple. The SDK clients have no Close method, so
// they are cached and shared to avoid leaking gRPC goroutines.
type clientEntry struct {
	naming naming_client.INamingClient
	config config_client.IConfigClient
}

var (
	clientMu    sync.Mutex
	clientCache = map[string]*clientEntry{}
)

// clientConfig builds the SDK ClientConfig from the application config.
func clientConfig(cfg *Config) constant.ClientConfig {
	return constant.ClientConfig{
		NamespaceId:         cfg.Namespace,
		TimeoutMs:           10000,
		NotLoadCacheAtStart: true,
		LogLevel:            "warn",
		Username:            cfg.Username,
		Password:            cfg.Password,
	}
}

// serverConfig parses "host:port" into an SDK ServerConfig.
func serverConfig(serverAddr string) (constant.ServerConfig, error) {
	host, portStr, err := net.SplitHostPort(serverAddr)
	if err != nil {
		return constant.ServerConfig{}, fmt.Errorf("invalid nacos serverAddr %q: %w", serverAddr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 64)
	if err != nil {
		return constant.ServerConfig{}, fmt.Errorf("invalid nacos server port %q: %w", portStr, err)
	}
	return *constant.NewServerConfig(host, port, constant.WithContextPath("/nacos")), nil
}

// buildClients creates a fresh naming + config client pair. Callers should go
// through getClients to obtain the cached, shared instance.
func buildClients(cfg *Config) (*clientEntry, error) {
	sc, err := serverConfig(cfg.ServerAddr)
	if err != nil {
		return nil, err
	}
	cc := clientConfig(cfg)
	param := vo.NacosClientParam{
		ClientConfig:  &cc,
		ServerConfigs: []constant.ServerConfig{sc},
	}
	naming, err := clients.NewNamingClient(param)
	if err != nil {
		return nil, fmt.Errorf("create nacos naming client: %w", err)
	}
	config, err := clients.NewConfigClient(param)
	if err != nil {
		return nil, fmt.Errorf("create nacos config client: %w", err)
	}
	return &clientEntry{naming: naming, config: config}, nil
}

// getClients returns the shared SDK clients for cfg, creating them on first use.
// On failure nothing is cached, so a later call (e.g. after the server recovers)
// retries creation.
func getClients(cfg *Config) (*clientEntry, error) {
	key := clientKey(cfg)
	clientMu.Lock()
	defer clientMu.Unlock()
	if e, ok := clientCache[key]; ok {
		return e, nil
	}
	e, err := buildClients(cfg)
	if err != nil {
		return nil, err
	}
	clientCache[key] = e
	return e, nil
}

func namingClientFor(cfg *Config) (naming_client.INamingClient, error) {
	e, err := getClients(cfg)
	if err != nil {
		return nil, err
	}
	return e.naming, nil
}

func configClientFor(cfg *Config) (config_client.IConfigClient, error) {
	e, err := getClients(cfg)
	if err != nil {
		return nil, err
	}
	return e.config, nil
}
