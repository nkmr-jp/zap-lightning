// Package zl provides zap based advanced logging features, and it's easy to use.
package zl

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/thoas/go-funk"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	consoleFieldDefault = "console"
)

var (
	once          sync.Once
	pretty        *prettyLogger
	zapLogger     *zap.Logger
	outputType    Output
	version       string
	severityLevel zapcore.Level // Default is InfoLevel
	callerEncoder zapcore.CallerEncoder
	consoleFields = []string{consoleFieldDefault}
	omitKeys      []Key
	isStdOut      bool
	separator     = " : "
	pid           int
)

// Init initializes the logger.
func Init() {
	once.Do(func() {
		if outputType == PrettyOutput {
			pretty = newPrettyLogger()
		}
		zapLogger = newZapLogger()
		var p string
		if pid != 0 {
			p = fmt.Sprintf(", PID: %d", pid)
		}
		Debug("INIT_LOGGER", Console(fmt.Sprintf(
			"Severity: %s, Output: %s, FileName: %s%s",
			severityLevel.CapitalString(),
			outputType.String(),
			fileName,
			p,
		)))
	})
}

// See https://pkg.go.dev/go.uber.org/zap
func newZapLogger() *zap.Logger {
	enc := zapcore.EncoderConfig{
		MessageKey:     string(MessageKey),
		LevelKey:       string(LevelKey),
		TimeKey:        string(TimeKey),
		NameKey:        string(LoggerKey),
		CallerKey:      string(CallerKey),
		FunctionKey:    string(FunctionKey),
		StacktraceKey:  string(StacktraceKey),
		EncodeLevel:    encodeLevel,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   getCallerEncoder(),
	}
	setOmitKeys(&enc)

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(enc),
		zapcore.NewMultiWriteSyncer(getSyncers()...),
		severityLevel,
	)
	return zap.New(core,
		zap.AddCallerSkip(1),
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
	).With(getAdditionalFields()...)
}

func encodeLevel(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	// See:
	// https://docs.python.org/3/library/logging.html#logging-levels
	// https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry#LogSeverity
	severityLevelStrings := map[zapcore.Level]string{
		zapcore.DebugLevel: "DEBUG",
		zapcore.InfoLevel:  "INFO",
		zapcore.WarnLevel:  "WARNING",
		zapcore.ErrorLevel: "ERROR",
		zapcore.FatalLevel: "CRITICAL",
	}
	enc.AppendString(severityLevelStrings[l])
}

func setOmitKeys(enc *zapcore.EncoderConfig) {
	for i := range omitKeys {
		switch omitKeys[i] {
		case MessageKey:
			enc.MessageKey = zapcore.OmitKey
		case LevelKey:
			enc.LevelKey = zapcore.OmitKey
		case TimeKey:
			enc.TimeKey = zapcore.OmitKey
		case LoggerKey:
			enc.NameKey = zapcore.OmitKey
		case CallerKey:
			enc.CallerKey = zapcore.OmitKey
		case FunctionKey:
			enc.FunctionKey = zapcore.OmitKey
		case StacktraceKey:
			enc.StacktraceKey = zapcore.OmitKey
		}
	}
}

func getAdditionalFields() (fields []zapcore.Field) {
	if !funk.Contains(omitKeys, VersionKey) {
		fields = append(fields, zap.String(string(VersionKey), GetVersion()))
	}
	if !funk.Contains(omitKeys, HostnameKey) {
		fields = append(fields, zap.String(string(HostnameKey), *getHost()))
	}
	if !funk.Contains(omitKeys, PIDKey) {
		pid = os.Getpid()
		fields = append(fields, zap.Int(string(PIDKey), pid))
	}
	return fields
}

// GetVersion return version when version is set.
// or return git commit hash when version is not set.
func GetVersion() string {
	if version != "" {
		return version
	}
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		return strings.TrimRight(string(out), "\n")
	}

	return "undefined"
}

// Sync logger of Zap's Sync.
// Note: If log output to console. error will occur (See: https://github.com/uber-go/zap/issues/880 )
func Sync() {
	if outputType != PrettyOutput && outputType != FileOutput {
		return
	}
	if err := zapLogger.Sync(); err != nil {
		log.Println(err)
	}
	pretty.printTraces()
}

// SyncWhenStop flush log buffer. when interrupt or terminated.
func SyncWhenStop() {
	if outputType != PrettyOutput && outputType != FileOutput {
		return
	}

	c := make(chan os.Signal, 1)

	go func() {
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		s := <-c

		sigCode := 0
		switch s.String() {
		case "interrupt":
			sigCode = 2
		case "terminated":
			sigCode = 15
		}

		Debug(fmt.Sprintf("GOT_SIGNAL_%v", strings.ToUpper(s.String())))
		Sync() // flush log buffer
		os.Exit(128 + sigCode)
	}()
}

func getHost() *string {
	ret, err := os.Hostname()
	if err != nil {
		log.Print(err)
		return nil
	}
	return &ret
}

func getCallerEncoder() zapcore.CallerEncoder {
	if callerEncoder != nil {
		return callerEncoder
	}
	return zapcore.ShortCallerEncoder
}

func getSyncers() (syncers []zapcore.WriteSyncer) {
	switch outputType {
	case PrettyOutput, FileOutput:
		syncers = append(syncers, zapcore.AddSync(newRotator()))
	case ConsoleAndFileOutput:
		syncers = append(syncers, zapcore.AddSync(getConsoleOutput()), zapcore.AddSync(newRotator()))
	case ConsoleOutput:
		syncers = append(syncers, zapcore.AddSync(getConsoleOutput()))
	}
	return
}

func getConsoleOutput() io.Writer {
	if isStdOut {
		return os.Stdout
	} else {
		return os.Stderr
	}
}

// Cleanup removes logger and resets settings. This is mainly used for testing etc.
func Cleanup() {
	once = sync.Once{}
	pretty = nil
	zapLogger = nil
	outputType = PrettyOutput
	version = ""
	severityLevel = zapcore.InfoLevel
	callerEncoder = nil
	consoleFields = []string{consoleFieldDefault}
	omitKeys = nil
	isStdOut = false
	separator = " : "

	fileName = ""
	maxSize = 0
	maxBackups = 0
	maxAge = 0
	localTime = false
	compress = false
}
