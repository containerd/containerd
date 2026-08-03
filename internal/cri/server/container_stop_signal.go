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

package server

import (
	"fmt"
	"strings"

	"github.com/containerd/errdefs"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func criSignalToOCIStopSignal(signal runtime.Signal) (string, error) {
	if signal == runtime.Signal_RUNTIME_DEFAULT {
		return "", nil
	}

	name, ok := runtime.Signal_name[int32(signal)]
	if !ok {
		return "", fmt.Errorf("unknown CRI stop signal %d: %w", signal, errdefs.ErrInvalidArgument)
	}
	return convertFromCRISignal(name), nil
}

func convertFromCRISignal(criSignal string) string {
	normalized := strings.Replace(criSignal, "PLUS", "+", 1)
	normalized = strings.Replace(normalized, "MINUS", "-", 1)
	return normalized
}

func toCRISignal(stopsignal string) runtime.Signal {
	stopsignal = strings.Replace(stopsignal, "+", "PLUS", 1)
	stopsignal = strings.Replace(stopsignal, "-", "MINUS", 1)

	signalValue, ok := runtime.Signal_value[stopsignal]
	if !ok {
		return runtime.Signal_RUNTIME_DEFAULT
	}
	return runtime.Signal(signalValue)
}
