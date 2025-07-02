package grab

import (
	"log/slog"
	"os"
)

func NewLogger(o Option) *slog.Logger {
	level := slog.LevelWarn
	if o.Debug {
		level = slog.LevelDebug
	}
	if o.Verbose {
		level = slog.LevelInfo
	}
	if o.Silent {
		level = slog.LevelError
	}
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: level <= slog.LevelDebug,
	})
	return slog.New(handler)
}
