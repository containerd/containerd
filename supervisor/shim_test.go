package supervisor

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseRuntimeLog(t *testing.T) {
	s := `{"level": "error", "msg": "foo\n", "time": "2017-01-01T00:00:42Z"}
{"level": "error", "msg": "bar\n", "time": "2017-01-01T00:00:43Z"}
`
	testCases := []struct {
		entries  int
		expected []map[string]interface{}
	}{
		{
			entries: 0,
			expected: []map[string]interface{}{
				map[string]interface{}{"level": "error", "msg": "foo\n", "time": "2017-01-01T00:00:42Z"},
				map[string]interface{}{"level": "error", "msg": "bar\n", "time": "2017-01-01T00:00:43Z"},
			},
		},
		{
			entries: 1,
			expected: []map[string]interface{}{
				map[string]interface{}{"level": "error", "msg": "bar\n", "time": "2017-01-01T00:00:43Z"}},
		},
		{
			entries: 2,
			expected: []map[string]interface{}{
				map[string]interface{}{"level": "error", "msg": "foo\n", "time": "2017-01-01T00:00:42Z"},
				map[string]interface{}{"level": "error", "msg": "bar\n", "time": "2017-01-01T00:00:43Z"},
			},
		},
	}

	for _, tc := range testCases {
		got, err := parseRuntimeLog(strings.NewReader(s), tc.entries)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tc.expected, got) {
			t.Fatalf("expected %v, got %v", tc.expected, got)
		}
	}
}
