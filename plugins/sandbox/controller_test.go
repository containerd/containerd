/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0
*/

package sandbox

import (
	"fmt"
	"testing"

	runtimeAPI "github.com/containerd/containerd/api/runtime/sandbox/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/ttrpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRestoredSandboxSpec(t *testing.T) {
	require.Nil(t, restoredSandboxSpec(nil))
	require.Nil(t, restoredSandboxSpec(&anypb.Any{}))

	spec := &anypb.Any{TypeUrl: "types.containerd.io/test.Sandbox", Value: []byte("spec")}
	require.Same(t, spec, restoredSandboxSpec(spec))
}

func TestValidatePreparedRestoreResponse(t *testing.T) {
	require.NoError(t, validatePreparedRestoreResponse("sandbox", &runtimeAPI.RestoreSandboxResponse{
		CreatedAt: timestamppb.Now(),
	}))

	err := validatePreparedRestoreResponse("sandbox", &runtimeAPI.RestoreSandboxResponse{
		CreatedAt: timestamppb.Now(),
		Tasks:     []*runtimeAPI.RestoredSandboxTask{{TaskID: "task"}},
	})
	require.ErrorIs(t, err, errdefs.ErrNotImplemented)

	err = validatePreparedRestoreResponse("sandbox", &runtimeAPI.RestoreSandboxResponse{})
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
}

func TestIgnorableSandboxShutdownError(t *testing.T) {
	for _, err := range []error{
		nil,
		errdefs.ErrNotFound,
		errdefs.ErrUnavailable,
		ttrpc.ErrClosed,
		fmt.Errorf("wrapped: %w", ttrpc.ErrClosed),
	} {
		require.True(t, ignorableSandboxShutdownError(err))
	}
	require.False(t, ignorableSandboxShutdownError(fmt.Errorf("shutdown failed")))
}
