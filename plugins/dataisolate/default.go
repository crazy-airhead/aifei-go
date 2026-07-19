package dataisolate

import "sync"

// Package-level default manager, mirroring plugins/storage's SetDefault pattern. Lets
// top-level helpers (Use, Sql) reach the started manager without passing it around.
var (
	defaultMgr *Manager
	defaultMu  sync.RWMutex
)

// SetDefault installs mgr as the package default (called by Plugin.Start).
func SetDefault(mgr *Manager) {
	defaultMu.Lock()
	defaultMgr = mgr
	defaultMu.Unlock()
}

// DefaultManager returns the package default manager, or nil before the plugin starts.
func DefaultManager() *Manager {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultMgr
}
