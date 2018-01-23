package log

import (
	"context"
	"testing"

	"github.com/gotestyourself/gotestyourself/assert"
	is "github.com/gotestyourself/gotestyourself/assert/cmp"
)

func TestLoggerContext(t *testing.T) {
	ctx := context.Background()
	assert.Check(t, is.DeepEqual(GetLogger(ctx), L))      // should be same as L variable
	assert.Check(t, is.DeepEqual(G(ctx), GetLogger(ctx))) // these should be the same.

	ctx = WithLogger(ctx, G(ctx).WithField("test", "one"))
	assert.Check(t, is.DeepEqual(GetLogger(ctx).Data["test"], "one"))
	assert.Check(t, is.DeepEqual(G(ctx), GetLogger(ctx))) // these should be the same.
}
