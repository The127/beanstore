package logging

import (
	"context"
	"log/slog"
)

type contextKey struct{}

type entry struct {
	logger *slog.Logger
	base   *slog.Logger
	scope  string
}

// FromContext returns the logger carried by ctx, or slog.Default.
func FromContext(ctx context.Context) *slog.Logger {
	e, ok := ctx.Value(contextKey{}).(entry)
	if !ok {
		return slog.Default()
	}

	return e.logger
}

// WithLogger derives a ctx that carries the given logger, shadowing
// whatever logger and scope the parent ctx carries.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, entry{
		logger: logger,
		base:   logger,
		scope:  "",
	})
}

// WithScope derives a ctx whose logger carries the given scope as a
// "scope" attribute. Scopes from the ctx chain join with dots.
func WithScope(ctx context.Context, name string) context.Context {
	e, ok := ctx.Value(contextKey{}).(entry)
	if !ok {
		e = entry{base: slog.Default()}
	}

	scope := name
	if e.scope != "" {
		scope = e.scope + "." + name
	}

	return context.WithValue(ctx, contextKey{}, entry{
		logger: e.base.With(slog.String("scope", scope)),
		base:   e.base,
		scope:  scope,
	})
}
