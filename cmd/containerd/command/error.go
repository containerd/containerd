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

package command

import (
	"github.com/pkg/errors"
)

var (
	// ErrUnknownLevel is returned when an unknown debugging level is encountered
	ErrUnknownLevel = errors.New("unknown level")
	// ErrRegisterAndUnregisterService is returned when both register and unregister flags are specified
	ErrRegisterAndUnregisterService = errors.New("--register-service and --unregister-service cannot be used together")
	// ErrEmptyTopic is returned when no topic is provided
	ErrEmptyTopic = errors.New("topic required to publish event")
	// ErrEmptyGRCPAddress is returned when the grpc address is empty
	ErrEmptyGRCPAddress = errors.New("grpc address cannot be empty")
)
