package log

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoggerContext(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, GetLogger(ctx), L)      // should be same as L variable
	assert.Equal(t, G(ctx), GetLogger(ctx)) // these should be the same.

	ctx = WithLogger(ctx, G(ctx).WithField("test", "one"))
	assert.Equal(t, GetLogger(ctx).Data["test"], "one")
	assert.Equal(t, G(ctx), GetLogger(ctx)) // these should be the same.
}
