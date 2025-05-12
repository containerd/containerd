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

/*
Copyright 2021 The Kubernetes Authors.

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

package responsewriter

import (
	"net/http"
)

// UserProvidedDecorator represensts a user (client that uses this package)
// provided decorator that wraps an inner http.ResponseWriter object.
// The user-provided decorator object must return the inner (decorated)
// http.ResponseWriter object via the Unwrap function.
type UserProvidedDecorator interface {
	http.ResponseWriter

	// Unwrap returns the inner http.ResponseWriter object associated
	// with the user-provided decorator.
	Unwrap() http.ResponseWriter
}

// GetOriginal goes through the chain of wrapped http.ResponseWriter objects
// and returns the original http.ResponseWriter object provided to the first
// request handler in the filter chain.
func GetOriginal(w http.ResponseWriter) http.ResponseWriter {
	decorator, ok := w.(UserProvidedDecorator)
	if !ok {
		return w
	}

	inner := decorator.Unwrap()
	if inner == w {
		// infinite cycle here, we should never be here though.
		panic("http.ResponseWriter decorator chain has a cycle")
	}

	return GetOriginal(inner)
}
