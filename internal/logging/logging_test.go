package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCapture() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestFromContextWithoutLoggerReturnsDefault(t *testing.T) {
	logger := FromContext(context.Background())

	assert.Same(t, slog.Default(), logger)
}

func TestWithLoggerRoundTrip(t *testing.T) {
	logger, _ := newCapture()

	ctx := WithLogger(context.Background(), logger)

	assert.Same(t, logger, FromContext(ctx))
}

func TestWithScopeAddsScopeAttribute(t *testing.T) {
	logger, buf := newCapture()
	ctx := WithScope(WithLogger(context.Background(), logger), "api")

	FromContext(ctx).Info("hello")

	assert.Contains(t, buf.String(), "scope=api")
}

func TestNestedScopesJoinWithDots(t *testing.T) {
	logger, buf := newCapture()
	ctx := WithLogger(context.Background(), logger)
	ctx = WithScope(ctx, "api")
	ctx = WithScope(ctx, "volume")

	FromContext(ctx).Info("hello")

	output := buf.String()
	require.Contains(t, output, "scope=api.volume")
	assert.Equal(t, 1, strings.Count(output, "scope="), "scope attribute must not stack")
}
