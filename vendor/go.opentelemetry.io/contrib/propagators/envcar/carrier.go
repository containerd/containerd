// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package envcar

import (
	"os"
	"strings"

	"go.opentelemetry.io/otel/propagation"
)

// Carrier is a [propagation.TextMapCarrier] for environment variables.
//
// [Carrier.Get] and [Carrier.Keys] read from the current process environment.
// [Carrier.Get] normalizes the key before lookup. [Carrier.Keys] only lists
// environment variable names that are already normalized.
//
// [Carrier.Set] writes through [Carrier.SetEnvFunc] with a normalized key. If
// SetEnvFunc is nil, [Carrier.Set] does nothing. This lets injection target the
// environment of a child process without mutating the current process
// environment. Using [os.Setenv] as SetEnvFunc is discouraged because
// applications should not modify their own context-related environment
// variables.
//
// Key name normalization is defined by the [OpenTelemetry specification].
//
// [OpenTelemetry specification]: https://opentelemetry.io/docs/specs/otel/context/env-carriers/#key-name-normalization
type Carrier struct {
	// SetEnvFunc sets an environment variable for injected context.
	// [Carrier.Set] calls SetEnvFunc with a normalized key.
	//
	// Set this to update the environment that will be passed to a child
	// process. Leave it nil for an extract-only carrier.
	SetEnvFunc func(key, value string)
}

// Compile time check that Carrier implements the TextMapCarrier.
var _ propagation.TextMapCarrier = (*Carrier)(nil)

// Get normalizes key and returns the corresponding value from the current
// process environment. It returns an empty string if the normalized
// environment variable is unset or set to an empty value.
//
// On platforms with case-insensitive environment lookup, such as Windows, the
// lookup may match an environment variable whose name differs from the
// normalized key only by case.
func (*Carrier) Get(key string) string {
	return os.Getenv(normalize(key))
}

// Set stores the key-value pair by calling [Carrier.SetEnvFunc].
// The key is normalized before SetEnvFunc is called.
//
// If SetEnvFunc is not set, this method does nothing.
func (c *Carrier) Set(key, value string) {
	if c.SetEnvFunc == nil {
		return
	}
	k := normalize(key)
	c.SetEnvFunc(k, value)
}

// Keys returns normalized environment variable names from the current process.
func (*Carrier) Keys() []string {
	environ := os.Environ()
	keys := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if !normalized(key) {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}
