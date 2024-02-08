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

package shim

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/containerd/ttrpc"
)

func TestChainUnaryServerInterceptors(t *testing.T) {
	methodInfo := &ttrpc.UnaryServerInfo{
		FullMethod: filepath.Join("/", t.Name(), "foo"),
	}

	type callKey struct{}
	callValue := "init"
	callCtx := context.WithValue(context.Background(), callKey{}, callValue)

	verifyCallCtxFn := func(ctx context.Context, key interface{}, expected interface{}) {
		got := ctx.Value(key)
		if !reflect.DeepEqual(expected, got) {
			t.Fatalf("[context(key:%s) expected %v, but got %v", key, expected, got)
		}
	}

	verifyInfoFn := func(info *ttrpc.UnaryServerInfo) {
		if !reflect.DeepEqual(methodInfo, info) {
			t.Fatalf("[info] expected %+v, but got %+v", methodInfo, info)
		}
	}

	origUnmarshaler := func(obj interface{}) error {
		v := obj.(*int64)
		*v *= 2
		return nil
	}

	type firstKey struct{}
	firstValue := "from first"
	var firstUnmarshaler ttrpc.Unmarshaler
	first := func(ctx context.Context, unmarshal ttrpc.Unmarshaler, info *ttrpc.UnaryServerInfo, method ttrpc.Method) (interface{}, error) {
		verifyCallCtxFn(ctx, callKey{}, callValue)
		verifyInfoFn(info)

		ctx = context.WithValue(ctx, firstKey{}, firstValue)

		firstUnmarshaler = func(obj interface{}) error {
			if err := unmarshal(obj); err != nil {
				return err
			}

			v := obj.(*int64)
			*v *= 2
			return nil
		}

		return method(ctx, firstUnmarshaler)
	}

	type secondKey struct{}
	secondValue := "from second"
	second := func(ctx context.Context, unmarshal ttrpc.Unmarshaler, info *ttrpc.UnaryServerInfo, method ttrpc.Method) (interface{}, error) {
		verifyCallCtxFn(ctx, callKey{}, callValue)
		verifyCallCtxFn(ctx, firstKey{}, firstValue)
		verifyInfoFn(info)

		v := int64(3) // should return 12
		if err := unmarshal(&v); err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if expected := int64(12); v != expected {
			t.Fatalf("expected int64(%v), but got %v", expected, v)
		}

		ctx = context.WithValue(ctx, secondKey{}, secondValue)
		return method(ctx, unmarshal)
	}

	methodFn := func(ctx context.Context, unmarshal func(interface{}) error) (interface{}, error) {
		verifyCallCtxFn(ctx, callKey{}, callValue)
		verifyCallCtxFn(ctx, firstKey{}, firstValue)
		verifyCallCtxFn(ctx, secondKey{}, secondValue)

		v := int64(2)
		if err := unmarshal(&v); err != nil {
			return nil, err
		}
		return v, nil
	}

	interceptor := chainUnaryServerInterceptors(first, second)
	v, err := interceptor(callCtx, origUnmarshaler, methodInfo, methodFn)
	if err != nil {
		t.Fatalf("expected nil, but got %v", err)
	}

	if expected := int64(8); v != expected {
		t.Fatalf("expected result is int64(%v), but got %v", expected, v)
	}
}
