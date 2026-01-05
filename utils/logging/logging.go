package logging

import (
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/phsym/console-slog"
)

// ConfigureSlog configures the default logger.
func ConfigureSlog(logLevel string) slog.Level {
	// We use debug level for trace-level logs.
	if logLevel == "trace" {
		logLevel = "debug"
	}
	var level slog.Level
	err := level.UnmarshalText([]byte(logLevel))
	if err != nil {
		log.Fatalf("invalid log level: %s", logLevel)
	}
	logger := newConsoleLogger(level <= slog.LevelDebug)
	slog.SetDefault(logger)
	return level
}

func newConsoleLogger(debug bool) *slog.Logger {
	level := slog.LevelInfo.Level()
	if debug {
		level = slog.LevelDebug.Level()
	}
	handler := console.NewHandler(os.Stdout, &console.HandlerOptions{
		Level:      level,
		TimeFormat: time.DateTime,
		Theme:      newTheme(),
	})
	return slog.New(handler)
}
