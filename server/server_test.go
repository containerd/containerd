package server

import (
	"testing"

	"github.com/gotestyourself/gotestyourself/assert"
	is "github.com/gotestyourself/gotestyourself/assert/cmp"
	"golang.org/x/net/context"
)

func TestNewErrorsWithSamePathForRootAndState(t *testing.T) {
	path := "/tmp/path/for/testing"
	_, err := New(context.Background(), &Config{
		Root:  path,
		State: path,
	})
	assert.Check(t, is.Error(err, "root and state must be different paths"))
}
