// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package envcar implements the
// [Environment Variables as Context Propagation Carriers specification].
//
// Environment variable propagation is intended for process boundaries where
// network protocols are not available, such as batch jobs, CI/CD, and command
// line tools. Treat context-related environment variables as process-startup
// input.
//
// Applications should extract context from the environment once during startup
// and then use the extracted context. Repeated extraction uses the process
// environment as a live parent-context source and may observe later environment
// variable changes.
//
// Note that environment variables can be visible to code in the same process
// and, on many systems, to other users or processes with sufficient
// permissions. Do not use this carrier for sensitive context.
//
// [Environment Variables as Context Propagation Carriers specification]: https://opentelemetry.io/docs/specs/otel/context/env-carriers/
package envcar
