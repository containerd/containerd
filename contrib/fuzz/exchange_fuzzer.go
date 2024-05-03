// Copyright 2021 ADA Logics Ltd
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package fuzz

import (
	"context"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/core/events/exchange"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

func FuzzExchange(data []byte) int {
	f := fuzz.NewConsumer(data)
	namespace, err := f.GetString()
	if err != nil {
		return 0
	}
	event := &eventstypes.ContainerCreate{}
	err = f.GenerateStruct(event)
	if err != nil {
		return 0
	}
	input, err := f.GetString()
	if err != nil {
		return 0
	}

	env := &events.Envelope{}
	err = f.GenerateStruct(env)
	if err != nil {
		return 0
	}
	ctx := namespaces.WithNamespace(context.Background(), namespace)
	exch := exchange.NewExchange()
	exch.Publish(ctx, input, event)
	exch.Forward(ctx, env)
	return 1
}
