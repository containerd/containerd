package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
)

func TestNewErrorsWithSamePathForRootAndState(t *testing.T) {
	path := "/tmp/path/for/testing"
	_, err := New(context.Background(), &Config{
		Root:  path,
		State: path,
	})
	assert.EqualError(t, err, "root and state must be different paths")
}
