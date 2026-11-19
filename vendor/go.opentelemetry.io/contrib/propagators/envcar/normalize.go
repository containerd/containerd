// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package envcar

import (
	"unicode/utf8"
)

// normalize converts s to a valid POSIX environment variable name.
// The conversion rules are:
//   - Empty input is converted to _.
//   - A–Z, 0–9, and _ are kept as-is.
//   - a–z are uppercased.
//   - All other characters are replaced with _.
//   - If the result would start with a digit, an underscore is prepended.
func normalize(s string) string {
	if s == "" {
		return "_"
	}

	// Pre-allocate the exact output length. If the first byte is a digit,
	// the name must be prefixed with '_', so allocate one extra byte.
	var b []byte
	i := 0
	if s[0] >= '0' && s[0] <= '9' {
		b = make([]byte, utf8.RuneCountInString(s)+1)
		b[0] = '_'
		i = 1
	} else {
		b = make([]byte, utf8.RuneCountInString(s))
	}

	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			// Uppercase letters, digits, and underscores are valid as-is.
			b[i] = byte(r) //nolint:gosec // G115: overflow is not possible.
		case r >= 'a' && r <= 'z':
			// Lowercase letters are converted to uppercase.
			b[i] = byte(r + 'A' - 'a')
		default:
			// All other characters (including non-ASCII runes) become underscores.
			b[i] = '_'
		}
		i++
	}
	return string(b)
}

// normalized reports whether s is already a non-empty normalized environment
// variable name.
func normalized(s string) bool {
	if s == "" {
		return false
	}
	// Normalized names cannot start with a digit; normalize would prepend '_'.
	if s[0] >= '0' && s[0] <= '9' {
		return false
	}

	for _, r := range s {
		switch {
		// A-Z, 0-9, and _ are already normalized.
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			// Lowercase letters and all other characters would be rewritten.
			return false
		}
	}
	return true
}
