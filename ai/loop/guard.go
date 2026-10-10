// guard.go 三模式互斥守卫（对标 MutualExclusionGuard）：内存 activeModes +
// 磁盘状态双检（Java 原义：无任何会话处于对立模式（内存）且本会话在对立
// 模式下无磁盘残留——同会话跨模式重启自挡）。
package loop

import (
	"sync"
	"time"
)

// MutualExclusionGuard 互斥守卫。
type MutualExclusionGuard struct {
	mu          sync.Mutex
	activeModes map[string]string // sessionID → mode
	disk        *DiskStateManager
}

// NewMutualExclusionGuard 构造。
func NewMutualExclusionGuard(disk *DiskStateManager) *MutualExclusionGuard {
	return &MutualExclusionGuard{activeModes: map[string]string{}, disk: disk}
}

// CanStartRalph 无任何 ultraqa 活跃且本会话无 ultraqa 磁盘残留。
func (g *MutualExclusionGuard) CanStartRalph(sessionID string) bool {
	return g.canStart("ultraqa", sessionID)
}

// CanStartUltraQA 无任何 ralph 活跃且本会话无 ralph 磁盘残留。
func (g *MutualExclusionGuard) CanStartUltraQA(sessionID string) bool {
	return g.canStart("ralph", sessionID)
}

// CanStartTeam ralph 与 ultraqa 均不活跃。
func (g *MutualExclusionGuard) CanStartTeam(sessionID string) bool {
	return g.canStart("ralph", sessionID) && g.canStart("ultraqa", sessionID)
}

func (g *MutualExclusionGuard) canStart(oppositeMode, sessionID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for sid, m := range g.activeModes {
		if m == oppositeMode && sid != sessionID {
			return false
		}
	}
	if g.disk != nil {
		if has, _ := g.disk.HasStateForMode(oppositeMode, sessionID); has {
			return false
		}
	}
	return true
}

// Acquire 获取互斥（按模式校验后登记；未知模式 false）。
func (g *MutualExclusionGuard) Acquire(sessionID, mode string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch mode {
	case "ralph":
		if !g.noActiveLocked("ultraqa", sessionID) {
			return false
		}
	case "ultraqa":
		if !g.noActiveLocked("ralph", sessionID) {
			return false
		}
	case "team":
		if !g.noActiveLocked("ralph", sessionID) || !g.noActiveLocked("ultraqa", sessionID) {
			return false
		}
	default:
		return false
	}
	g.activeModes[sessionID] = mode
	return true
}

func (g *MutualExclusionGuard) noActiveLocked(mode, exclude string) bool {
	for sid, m := range g.activeModes {
		if m == mode && sid != exclude {
			return false
		}
	}
	return true
}

// Release 释放。
func (g *MutualExclusionGuard) Release(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.activeModes, sessionID)
}

// ForceRelease 强制释放（含磁盘清态；恒 true——Java 同款）。
func (g *MutualExclusionGuard) ForceRelease(mode, sessionID string) bool {
	g.mu.Lock()
	if g.activeModes[sessionID] == mode {
		delete(g.activeModes, sessionID)
	}
	g.mu.Unlock()
	if g.disk != nil {
		g.disk.ClearState(mode, sessionID)
	}
	return true
}

// ActiveMode 会话活跃模式。
func (g *MutualExclusionGuard) ActiveMode(sessionID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeModes[sessionID]
}

// ActiveModesDescription 活跃模式描述。
func (g *MutualExclusionGuard) ActiveModesDescription(excludeSessionID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for sid, m := range g.activeModes {
		if sid != excludeSessionID {
			out = append(out, m+":"+sid)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	desc := ""
	for i, s := range out {
		if i > 0 {
			desc += ", "
		}
		desc += s
	}
	return desc
}

// IsActive 指定模式会话活跃。
func (g *MutualExclusionGuard) IsActive(mode, sessionID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeModes[sessionID] == mode
}

// IsAnyActive 任一模式活跃。
func (g *MutualExclusionGuard) IsAnyActive() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.activeModes) > 0
}

// ActiveCount 活跃数。
func (g *MutualExclusionGuard) ActiveCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.activeModes)
}

// CleanupStaleLocks 超龄磁盘锁清理（磁盘有记录而内存无、超龄或不可读则清）。
func (g *MutualExclusionGuard) CleanupStaleLocks(maxAgeMs int64) {
	if g.disk == nil {
		return
	}
	ids, _ := g.disk.AllSessionIDs()
	for _, id := range ids {
		g.mu.Lock()
		_, active := g.activeModes[id]
		g.mu.Unlock()
		if active {
			continue
		}
		data, _ := g.disk.LoadState(id)
		if data == nil {
			g.disk.ClearState("", id)
			continue
		}
		age := time.Since(data.LastUpdateTime).Milliseconds()
		if age < 0 {
			age = -age
		}
		if age > maxAgeMs {
			g.disk.ClearState("", id)
		}
	}
}
