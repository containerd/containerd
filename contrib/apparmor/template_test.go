//go:build linux

package apparmor

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)

	profile := buf.String()
	// Where AppArmor stacking is enabled, exec'd processes get a stacked
	// profile (e.g. "cri-containerd.apparmor.d//&unconfined"). Stacked label
	// components are ordered lexically rather than by stacking order, so the
	// profile can land at either end or in the middle of the label; each
	// position needs its own rule. The glob must be ** and not *, since *
	// stops at "/" and the remaining components can contain one.
	for _, rule := range []string{
		"signal (send,receive) peer=%s,",
		"ptrace (trace,tracedby,read,readby) peer=%s,",
	} {
		for _, peer := range []string{
			"cri-containerd.apparmor.d//&**",
			"**//&cri-containerd.apparmor.d",
			"**//&cri-containerd.apparmor.d//&**",
		} {
			assert.Contains(t, profile, fmt.Sprintf(rule, peer))
		}
	}
}
