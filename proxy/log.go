package proxy

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// Logger provides proxy-level logging.
type Logger struct {
	debug  *log.Logger
	info   *log.Logger
	warn   *log.Logger
	error  *log.Logger
	mu     sync.RWMutex
	file   io.WriteCloser
}

var (
	defaultLogger *Logger
	logOnce       sync.Once
)

func initLogger() {
	defaultLogger = NewLogger("minomac_proxy.log")
}

// NewLogger creates a new proxy logger writing to both file and stderr.
func NewLogger(logPath string) *Logger {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		f = nil
	}

	var logWriter io.Writer
	if f != nil {
		logWriter = io.MultiWriter(os.Stderr, f)
	} else {
		logWriter = os.Stderr
	}

	flags := log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile

	return &Logger{
		debug: log.New(logWriter, "[DEBUG] ", flags),
		info:  log.New(logWriter, "[INFO]  ", flags),
		warn:  log.New(logWriter, "[WARN]  ", flags),
		error: log.New(logWriter, "[ERROR] ", flags),
		file:  f,
	}
}

// Debug logs a debug message with format.
func (l *Logger) Debug(format string, args ...interface{}) {
	l.debug.Output(2, fmt.Sprintf(format, args...))
}

// Info logs an info message with format.
func (l *Logger) Info(format string, args ...interface{}) {
	l.info.Output(2, fmt.Sprintf(format, args...))
}

// Warn logs a warning message with format.
func (l *Logger) Warn(format string, args ...interface{}) {
	l.warn.Output(2, fmt.Sprintf(format, args...))
}

// Error logs an error message with format.
func (l *Logger) Error(format string, args ...interface{}) {
	l.error.Output(2, fmt.Sprintf(format, args...))
}

// Close closes the log file.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
	}
}

// Log returns the default logger.
func Log() *Logger {
	logOnce.Do(initLogger)
	return defaultLogger
}

// LogEvent is a structured log event for proxy operations.
type LogEvent struct {
	Time    time.Time
	Level   string
	ConnID  string
	Event   string
	Details string
}

// ConnLogger provides per-connection logging with a unique connection ID.
type ConnLogger struct {
	ID     string
	logger *Logger
}

// NewConnLogger creates a new connection-specific logger.
func NewConnLogger(remoteAddr string) *ConnLogger {
	return &ConnLogger{
		ID:     fmt.Sprintf("[%s]", remoteAddr),
		logger: Log(),
	}
}

func (cl *ConnLogger) Debug(format string, args ...interface{}) {
	cl.logger.debug.Output(2, cl.ID+" "+fmt.Sprintf(format, args...))
}

func (cl *ConnLogger) Info(format string, args ...interface{}) {
	cl.logger.info.Output(2, cl.ID+" "+fmt.Sprintf(format, args...))
}

func (cl *ConnLogger) Warn(format string, args ...interface{}) {
	cl.logger.warn.Output(2, cl.ID+" "+fmt.Sprintf(format, args...))
}

func (cl *ConnLogger) Error(format string, args ...interface{}) {
	cl.logger.error.Output(2, cl.ID+" "+fmt.Sprintf(format, args...))
}
