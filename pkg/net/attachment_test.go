/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package net

import (
	"context"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttachmentFromRecord(t *testing.T) {
	rec := testAttachmentRecord(t)
	att := attachmentFromRecord(rec, testCNI(t), testMemStore(t))
	assert.NotNil(t, att)
	assert.Equal(t, att.attachmentRecord, *rec)
}

func TestAttachmentObject(t *testing.T) {
	obj := testAttachment(t)
	assert.Equal(t, obj.ID(), TestAttachment)
	assert.Equal(t, obj.Manager(), TestManager)
	assert.Equal(t, obj.Network(), TestNetwork)
	assert.Equal(t, obj.Container(), TestContainer)
	assert.Equal(t, obj.IFName(), TestInterface)
	assert.Equal(t, obj.NSPath(), TestNSPath)
	if assert.Contains(t, obj.GCOwnerLables(), TestGCLBKey) {
		assert.Equal(t, obj.GCOwnerLables()[TestGCLBKey], TestGCLBVal)
	}
}

func TestAttachmentRemove(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	att := testAttachment(t)

	c, ok := att.cni.(*mockCNI)
	require.True(t, ok)
	s, ok := att.store.(*mockStore)
	require.True(t, ok)

	c.err = nil
	assert.Error(t, att.Remove(ctx))

	c.err = nil
	s.atts[att.id] = &att.attachmentRecord
	assert.NoError(t, att.Remove(ctx))
	assert.NotContains(t, s.atts, att.id)

	c.err = errdefs.ErrNotFound
	s.atts[att.id] = &att.attachmentRecord
	assert.NoError(t, att.Remove(ctx))
	assert.NotContains(t, s.atts, att.id)

	c.err = errdefs.ErrUnknown
	s.atts[att.id] = &att.attachmentRecord
	assert.Error(t, att.Remove(ctx))
	assert.Contains(t, s.atts, att.id)
}

func TestAttachmentCheck(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	att := testAttachment(t)

	c, ok := att.cni.(*mockCNI)
	require.True(t, ok)

	c.err = nil
	assert.NoError(t, att.Check(ctx))

	c.err = errdefs.ErrUnknown
	assert.Error(t, att.Check(ctx))
}

func testAttachment(t *testing.T) *attachment {
	return attachmentFromRecord(testAttachmentRecord(t), testCNI(t), testMemStore(t))
}
