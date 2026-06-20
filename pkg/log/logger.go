package log

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/grpclog"
)

// Colors ANSI
const (
	Reset      = "\033[0m"
	BrightGray = "\033[37m"
	Green      = "\033[32m"
	Yellow     = "\033[33m"
	Red        = "\033[31m"
	BoldRed    = "\033[31;1m"
)

// Predefine log levels
const (
	INFO    = "INFO"
	WARNING = "WARNING"
	ERROR   = "ERROR"
	FATAL   = "FATAL"
)

var contextMap sync.Map

// SetContextID sets the context ID for the current goroutine.
func SetContextID(ctxID string) {
	gid := getGoroutineID()
	contextMap.Store(gid, ctxID)
}

// getContextID returns the context ID for the current goroutine.
func getContextID() string {
	gid := getGoroutineID()
	if ctxID, ok := contextMap.Load(gid); ok {
		return ctxID.(string)
	}
	return "unknown" // Return "unknown" if the context ID is not found
}

// getGoroutineID returns the ID of the current goroutine.
func getGoroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	var gid int
	_, err := fmt.Sscanf(string(buf[:n]), "goroutine %d ", &gid)
	if err != nil {
		return 0
	}
	return gid
}

// CustomLogger implements grpclog.LoggerV2 with optimizations.
type CustomLogger struct {
	writer     *bufio.Writer
	mutex      sync.Mutex
	component  string
	timestamp  string
	stopTicker chan struct{}
}

// Global logger instance
var loggerV2Impl grpclog.LoggerV2 = NewCustomLogger("default")

// NewCustomLogger initializes a new CustomLogger.
func NewCustomLogger(component string) *CustomLogger {
	cl := &CustomLogger{
		writer:     bufio.NewWriter(os.Stdout),
		component:  component,
		stopTicker: make(chan struct{}),
	}

	cl.timestamp = time.Now().Format("2006-01-02 15:04:05.000 -07:00")

	go cl.updateTimestamp()

	return cl
}

// updateTimestamp updates the timestamp every 100 millisecond.
func (cl *CustomLogger) updateTimestamp() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cl.mutex.Lock()
			cl.timestamp = time.Now().Format("2006-01-02 15:04:05.000 -07:00")
			cl.mutex.Unlock()
		case <-cl.stopTicker:
			return
		}
	}
}

// log logs a message with the specified level, color, and message.
func (cl *CustomLogger) log(level string, color string, msg string) {
	// get context id
	ctxID := getContextID()

	cl.mutex.Lock()
	defer cl.mutex.Unlock()
	_, err := fmt.Fprintf(
		cl.writer,
		"%s[%s]%s  %s%s%s [%s] [ctx_id=%s] %s\n",
		BrightGray, cl.timestamp, Reset,
		color, level, Reset,
		cl.component, ctxID, msg,
	)
	if err != nil {
		return
	}
	err = cl.writer.Flush()
	if err != nil {
		return
	}
}

func isGRPCInternalLog(msg string) bool {
	return strings.HasPrefix(msg, "[transport]") || strings.HasPrefix(msg, "[core]")
}

func (cl *CustomLogger) Info(args ...any) {
	msg := fmt.Sprint(args...)
	if isGRPCInternalLog(msg) {
		return
	}
	cl.log(INFO, Green, msg)
}

func (cl *CustomLogger) Infoln(args ...any) {
	msg := fmt.Sprintln(args...)
	if isGRPCInternalLog(msg) {
		return
	}
	cl.log(INFO, Green, msg)
}

func (cl *CustomLogger) Infof(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if isGRPCInternalLog(msg) {
		return
	}
	cl.log(INFO, Green, msg)
}

func (cl *CustomLogger) Warning(args ...any) {
	cl.log(WARNING, Yellow, fmt.Sprint(args...))
}

func (cl *CustomLogger) Warningln(args ...any) {
	cl.log(WARNING, Yellow, fmt.Sprintln(args...))
}

func (cl *CustomLogger) Warningf(format string, args ...any) {
	cl.log(WARNING, Yellow, fmt.Sprintf(format, args...))
}

func (cl *CustomLogger) Error(args ...any) {
	cl.log(ERROR, Red, fmt.Sprint(args...))
}

func (cl *CustomLogger) Errorln(args ...any) {
	cl.log(ERROR, Red, fmt.Sprintln(args...))
}

func (cl *CustomLogger) Errorf(format string, args ...any) {
	cl.log(ERROR, Red, fmt.Sprintf(format, args...))
}

func (cl *CustomLogger) Fatal(args ...any) {
	cl.log(FATAL, BoldRed, fmt.Sprint(args...))
	os.Exit(1)
}

func (cl *CustomLogger) Fatalln(args ...any) {
	cl.log(FATAL, BoldRed, fmt.Sprintln(args...))
	os.Exit(1)
}

func (cl *CustomLogger) Fatalf(format string, args ...any) {
	cl.log(FATAL, BoldRed, fmt.Sprintf(format, args...))
	os.Exit(1)
}

func (cl *CustomLogger) V(level int) bool {
	return true
}

// InfoDepth Depth methods for grpclog.LoggerV2 interface
func (cl *CustomLogger) InfoDepth(depth int, args ...any)    { cl.Info(args...) }
func (cl *CustomLogger) WarningDepth(depth int, args ...any) { cl.Warning(args...) }
func (cl *CustomLogger) ErrorDepth(depth int, args ...any)   { cl.Error(args...) }
func (cl *CustomLogger) FatalDepth(depth int, args ...any)   { cl.Fatal(args...) }

// Stop stops the background timestamp updater.
func (cl *CustomLogger) Stop() {
	close(cl.stopTicker)
}

// InitLogger initializes the global logger with the specified component name.
func InitLogger(component string) {
	logger := NewCustomLogger(component)
	loggerV2Impl = logger
	grpclog.SetLoggerV2(loggerV2Impl) // Set custom logger for gRPC logs
}

// Public logging functions

func Info(args ...any) {
	loggerV2Impl.Info(args...)
}

func Infof(format string, args ...any) {
	loggerV2Impl.Infof(format, args...)
}

func Warning(args ...any) {
	loggerV2Impl.Warning(args...)
}

func Warningf(format string, args ...any) {
	loggerV2Impl.Warningf(format, args...)
}

func Warningln(format string, args ...any) {
	loggerV2Impl.Warningln(args...)
}

func Error(args ...any) {
	loggerV2Impl.Error(args...)
}

func Errorf(format string, args ...any) {
	loggerV2Impl.Errorf(format, args...)
}

func Errorln(format string, args ...any) {
	loggerV2Impl.Errorln(args...)
}

func Fatal(args ...any) {
	loggerV2Impl.Fatal(args...)
}

func Fatalf(format string, args ...any) {
	loggerV2Impl.Fatalf(format, args...)
}
