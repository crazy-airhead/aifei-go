package log

import (
	"fmt"
	"log"
	"os"
)

// Level represents a log level.
type Level int

const (
	LevelTrace Level = iota
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
	LevelOff
)

// Logger is the logging interface.
type Logger interface {
	Trace(msg string, args ...interface{})
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	IsTraceEnabled() bool
	IsDebugEnabled() bool
	IsInfoEnabled() bool
	IsWarnEnabled() bool
	IsErrorEnabled() bool
}

type defaultLogger struct {
	level  Level
	logger *log.Logger
}

func (l *defaultLogger) Trace(msg string, args ...interface{}) {
	if l.level <= LevelTrace {
		l.logger.Printf("[TRACE] %s", fmt.Sprintf(msg, args...))
	}
}

func (l *defaultLogger) Debug(msg string, args ...interface{}) {
	if l.level <= LevelDebug {
		l.logger.Printf("[DEBUG] %s", fmt.Sprintf(msg, args...))
	}
}

func (l *defaultLogger) Info(msg string, args ...interface{}) {
	if l.level <= LevelInfo {
		l.logger.Printf("[INFO]  %s", fmt.Sprintf(msg, args...))
	}
}

func (l *defaultLogger) Warn(msg string, args ...interface{}) {
	if l.level <= LevelWarn {
		l.logger.Printf("[WARN]  %s", fmt.Sprintf(msg, args...))
	}
}

func (l *defaultLogger) Error(msg string, args ...interface{}) {
	if l.level <= LevelError {
		l.logger.Printf("[ERROR] %s", fmt.Sprintf(msg, args...))
	}
}

func (l *defaultLogger) IsTraceEnabled() bool { return l.level <= LevelTrace }
func (l *defaultLogger) IsDebugEnabled() bool { return l.level <= LevelDebug }
func (l *defaultLogger) IsInfoEnabled() bool  { return l.level <= LevelInfo }
func (l *defaultLogger) IsWarnEnabled() bool  { return l.level <= LevelWarn }
func (l *defaultLogger) IsErrorEnabled() bool { return l.level <= LevelError }

var defaultLog Logger

func init() {
	defaultLog = &defaultLogger{
		level:  LevelInfo,
		logger: log.New(os.Stdout, "", log.LstdFlags|log.Lshortfile),
	}
}

// Default returns the default logger.
func Default() Logger { return defaultLog }

// SetDefault sets the default logger.
func SetDefault(l Logger) { defaultLog = l }

// SetLevel sets the log level.
func SetLevel(level Level) {
	if dl, ok := defaultLog.(*defaultLogger); ok {
		dl.level = level
	}
}

// Trace logs at trace level.
func Trace(msg string, args ...interface{}) { defaultLog.Trace(msg, args...) }

// Debug logs at debug level.
func Debug(msg string, args ...interface{}) { defaultLog.Debug(msg, args...) }

// Info logs at info level.
func Info(msg string, args ...interface{}) { defaultLog.Info(msg, args...) }

// Warn logs at warn level.
func Warn(msg string, args ...interface{}) { defaultLog.Warn(msg, args...) }

// Error logs at error level.
func Error(msg string, args ...interface{}) { defaultLog.Error(msg, args...) }
