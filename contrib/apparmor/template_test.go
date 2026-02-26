//go:build linux

package apparmor

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanProfileName(t *testing.T) {
	assert.Equal(t, cleanProfileName(""), "unconfined")
	assert.Equal(t, cleanProfileName("unconfined"), "unconfined")
	assert.Equal(t, cleanProfileName("unconfined (enforce)"), "unconfined")
	assert.Equal(t, cleanProfileName("docker-default"), "docker-default")
	assert.Equal(t, cleanProfileName("foo"), "foo")
	assert.Equal(t, cleanProfileName("foo (enforce)"), "foo")
}

func TestGenerateStackedProfileRules(t *testing.T) {
	p := &data{
		Name:          "cri-containerd.apparmor.d",
		DaemonProfile: "unconfined",
	}
	var buf bytes.Buffer
	err := generate(p, &buf)
	assert.NoError(t, err)

	profile := buf.String()
	// Verify stacking rules for signal and ptrace are present.
	// On kernel 6.7+ with AppArmor stacking, exec'd processes get a
	// stacked profile (e.g. "cri-containerd.apparmor.d//&unconfined").
	assert.Contains(t, profile, "signal (send,receive) peer=cri-containerd.apparmor.d//&*,")
	assert.Contains(t, profile, "ptrace (trace,tracedby,read,readby) peer=cri-containerd.apparmor.d//&*,")
}
