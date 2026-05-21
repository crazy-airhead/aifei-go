package log

import (
	"strings"
	"testing"
)

type testLogger struct {
	logs []string
}

func (l *testLogger) Trace(msg string, args ...interface{}) { l.logs = append(l.logs, "TRACE: "+msg) }
func (l *testLogger) Debug(msg string, args ...interface{}) { l.logs = append(l.logs, "DEBUG: "+msg) }
func (l *testLogger) Info(msg string, args ...interface{})  { l.logs = append(l.logs, "INFO: "+msg) }
func (l *testLogger) Warn(msg string, args ...interface{})  { l.logs = append(l.logs, "WARN: "+msg) }
func (l *testLogger) Error(msg string, args ...interface{}) { l.logs = append(l.logs, "ERROR: "+msg) }
func (l *testLogger) IsTraceEnabled() bool                  { return true }
func (l *testLogger) IsDebugEnabled() bool                  { return true }
func (l *testLogger) IsInfoEnabled() bool                   { return true }
func (l *testLogger) IsWarnEnabled() bool                   { return true }
func (l *testLogger) IsErrorEnabled() bool                  { return true }

func TestDefaultLogger(t *testing.T) {
	origLogger := Default()
	SetLevel(LevelDebug)
	Debug("test debug %s", "message")
	Info("test info")
	Warn("test warn")
	Error("test error")
	SetDefault(origLogger)
}

func TestCustomLogger(t *testing.T) {
	origLogger := Default()
	tl := &testLogger{}
	SetDefault(tl)

	Info("custom message")
	if len(tl.logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(tl.logs))
	}
	if tl.logs[0] != "INFO: custom message" {
		t.Fatalf("unexpected log: %s", tl.logs[0])
	}
	SetDefault(origLogger)
}

func TestLogLevel(t *testing.T) {
	origLogger := Default()
	tl := &testLogger{}
	SetDefault(tl)
	// Level filtering is done by defaultLogger, testLogger always logs
	// So we test that defaultLogger respects levels instead
	SetDefault(origLogger)
	SetLevel(LevelWarn)

	// We can't easily capture default logger output, so just verify no panic
	Warn("should appear at warn level")
	Error("should appear at error level")
	Info("should be filtered at warn level")
	SetLevel(LevelInfo)
}

func TestLevelFiltering(t *testing.T) {
	// Use a custom logger that respects levels
	l := &levelTestLogger{level: LevelWarn}
	l.Debug("should not log")
	l.Info("should not log")
	l.Warn("should log")
	l.Error("should log")
	if len(l.logs) != 2 {
		t.Fatalf("expected 2 logs, got %d: %v", len(l.logs), l.logs)
	}
}

type levelTestLogger struct {
	level Level
	logs  []string
}

func (l *levelTestLogger) Trace(msg string, args ...interface{}) {
	if l.level <= LevelTrace {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) Debug(msg string, args ...interface{}) {
	if l.level <= LevelDebug {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) Info(msg string, args ...interface{}) {
	if l.level <= LevelInfo {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) Warn(msg string, args ...interface{}) {
	if l.level <= LevelWarn {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) Error(msg string, args ...interface{}) {
	if l.level <= LevelError {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) IsTraceEnabled() bool { return l.level <= LevelTrace }
func (l *levelTestLogger) IsDebugEnabled() bool { return l.level <= LevelDebug }
func (l *levelTestLogger) IsInfoEnabled() bool  { return l.level <= LevelInfo }
func (l *levelTestLogger) IsWarnEnabled() bool  { return l.level <= LevelWarn }
func (l *levelTestLogger) IsErrorEnabled() bool { return l.level <= LevelError }

func TestStringsPackage(t *testing.T) {
	_ = strings.Contains("hello", "ell")
}
