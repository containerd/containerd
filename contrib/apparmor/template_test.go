//go:build linux

package apparmor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanProfileName(t *testing.T) {
	assert.Equal(t, "unconfined", cleanProfileName(""))
	assert.Equal(t, "unconfined", cleanProfileName("unconfined"))
	assert.Equal(t, "unconfined", cleanProfileName("unconfined\n"))
	assert.Equal(t, "unconfined", cleanProfileName("unconfined (enforce)"))
	assert.Equal(t, "unconfined", cleanProfileName("unconfined (enforce)\n"))
	assert.Equal(t, "docker-default", cleanProfileName("docker-default"))
	assert.Equal(t, "foo", cleanProfileName("foo"))
	assert.Equal(t, "foo", cleanProfileName("foo (enforce)"))
	assert.Equal(t, "with spaces", cleanProfileName("with spaces"))
	assert.Equal(t, "with spaces", cleanProfileName("with spaces (enforce)"))
}
