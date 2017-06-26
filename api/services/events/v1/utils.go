package events

import "strings"

func checkMap(fieldpath []string, m map[string]string) (string, bool) {
	if len(m) == 0 {
		return "", false
	}
	value, ok := m[strings.Join(fieldpath, ".")]
	return value, ok
}
