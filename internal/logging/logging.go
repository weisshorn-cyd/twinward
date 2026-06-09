package logging

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Configure installs slog as both the application and controller-runtime logger.
func Configure(level slog.Leveler) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)

	slog.SetDefault(logger)
	ctrl.SetLogger(logr.FromSlogHandler(handler))
	klog.SetSlogLogger(logger)

	return logger
}

// FromContext returns the controller-runtime logger as a slog logger.
func FromContext(ctx context.Context) *slog.Logger {
	return slog.New(logr.ToSlogHandler(ctrl.LoggerFrom(ctx)))
}
