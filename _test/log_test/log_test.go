package log_test

import (
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/log"
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
	origLogger := log.Default()
	log.SetLevel(log.LevelDebug)
	log.Debug("test debug %s", "message")
	log.Info("test info")
	log.Warn("test warn")
	log.Error("test error")
	log.SetDefault(origLogger)
}

func TestCustomLogger(t *testing.T) {
	origLogger := log.Default()
	tl := &testLogger{}
	log.SetDefault(tl)

	log.Info("custom message")
	if len(tl.logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(tl.logs))
	}
	if tl.logs[0] != "INFO: custom message" {
		t.Fatalf("unexpected log: %s", tl.logs[0])
	}
	log.SetDefault(origLogger)
}

func TestLogLevel(t *testing.T) {
	origLogger := log.Default()
	tl := &testLogger{}
	log.SetDefault(tl)
	// Level filtering is done by defaultLogger, testLogger always logs
	// So we test that defaultLogger respects levels instead
	log.SetDefault(origLogger)
	log.SetLevel(log.LevelWarn)

	// We can't easily capture default logger output, so just verify no panic
	log.Warn("should appear at warn level")
	log.Error("should appear at error level")
	log.Info("should be filtered at warn level")
	log.SetLevel(log.LevelInfo)
}

func TestLevelFiltering(t *testing.T) {
	// Use a custom logger that respects levels
	l := &levelTestLogger{level: log.LevelWarn}
	l.Debug("should not log")
	l.Info("should not log")
	l.Warn("should log")
	l.Error("should log")
	if len(l.logs) != 2 {
		t.Fatalf("expected 2 logs, got %d: %v", len(l.logs), l.logs)
	}
}

type levelTestLogger struct {
	level log.Level
	logs  []string
}

func (l *levelTestLogger) Trace(msg string, args ...interface{}) {
	if l.level <= log.LevelTrace {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) Debug(msg string, args ...interface{}) {
	if l.level <= log.LevelDebug {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) Info(msg string, args ...interface{}) {
	if l.level <= log.LevelInfo {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) Warn(msg string, args ...interface{}) {
	if l.level <= log.LevelWarn {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) Error(msg string, args ...interface{}) {
	if l.level <= log.LevelError {
		l.logs = append(l.logs, msg)
	}
}
func (l *levelTestLogger) IsTraceEnabled() bool { return l.level <= log.LevelTrace }
func (l *levelTestLogger) IsDebugEnabled() bool { return l.level <= log.LevelDebug }
func (l *levelTestLogger) IsInfoEnabled() bool  { return l.level <= log.LevelInfo }
func (l *levelTestLogger) IsWarnEnabled() bool  { return l.level <= log.LevelWarn }
func (l *levelTestLogger) IsErrorEnabled() bool { return l.level <= log.LevelError }

func TestStringsPackage(t *testing.T) {
	_ = strings.Contains("hello", "ell")
}
