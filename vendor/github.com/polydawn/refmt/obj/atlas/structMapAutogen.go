package atlas

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode"
)

func AutogenerateStructMapEntry(rt reflect.Type) *AtlasEntry {
	return AutogenerateStructMapEntryUsingTags(rt, "refmt", KeySortMode_Default)
}

func AutogenerateStructMapEntryUsingTags(rt reflect.Type, tagName string, sorter KeySortMode) *AtlasEntry {
	if rt.Kind() != reflect.Struct {
		panic(fmt.Errorf("cannot use structMap for type %q, which is kind %s", rt, rt.Kind()))
	}
	entry := &AtlasEntry{
		Type:      rt,
		StructMap: &StructMap{Fields: exploreFields(rt, tagName, sorter)},
	}
	return entry
}

// exploreFields returns a list of fields that StructAtlas should recognize for the given type.
// The algorithm is breadth-first search over the set of structs to include - the top struct
// and then any reachable anonymous structs.
func exploreFields(rt reflect.Type, tagName string, sorter KeySortMode) []StructMapEntry {
	// Anonymous fields to explore at the current level and the next.
	current := []StructMapEntry{}
	next := []StructMapEntry{{Type: rt}}

	// Count of queued names for current level and the next.
	count := map[reflect.Type]int{}
	nextCount := map[reflect.Type]int{}

	// Types already visited at an earlier level.
	visited := map[reflect.Type]bool{}

	// Fields found.
	var fields []StructMapEntry

	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}

		for _, f := range current {
			if visited[f.Type] {
				continue
			}
			visited[f.Type] = true

			// Scan f.Type for fields to include.
			for i := 0; i < f.Type.NumField(); i++ {
				sf := f.Type.Field(i)
				if sf.PkgPath != "" && !sf.Anonymous { // unexported
					continue
				}
				tag := sf.Tag.Get(tagName)
				if tag == "-" {
					continue
				}
				name, opts := parseTag(tag)
				if !isValidTag(name) {
					name = ""
				}
				route := make([]int, len(f.ReflectRoute)+1)
				copy(route, f.ReflectRoute)
				route[len(f.ReflectRoute)] = i

				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Ptr {
					// Follow pointer.
					ft = ft.Elem()
				}

				// Record found field and index sequence.
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = downcaseFirstLetter(sf.Name)
					}
					fields = append(fields, StructMapEntry{
						SerialName:   name,
						ReflectRoute: route,
						Type:         sf.Type,
						tagged:       tagged,
						OmitEmpty:    opts.Contains("omitempty"),
					})
					if count[f.Type] > 1 {
						// If there were multiple instances, add a second,
						// so that the annihilation code will see a duplicate.
						// It only cares about the distinction between 1 or 2,
						// so don't bother generating any more copies.
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}

				// Record new anonymous struct to explore in next round.
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, StructMapEntry{
						ReflectRoute: route,
						Type:         ft,
					})
				}
			}
		}
	}

	sort.Sort(StructMapEntry_byName(fields))

	// Delete all fields that are hidden by the Go rules for embedded fields,
	// except that fields with JSON tags are promoted.

	// The fields are sorted in primary order of name, secondary order
	// of field index length. Loop over names; for each name, delete
	// hidden fields by choosing the one dominant field that survives.
	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		// One iteration per name.
		// Find the sequence of fields with the name of this first field.
		fi := fields[i]
		name := fi.SerialName
		for advance = 1; i+advance < len(fields); advance++ {
			fj := fields[i+advance]
			if fj.SerialName != name {
				break
			}
		}
		if advance == 1 { // Only one field with this name
			out = append(out, fi)
			continue
		}
		dominant, ok := dominantField(fields[i : i+advance])
		if ok {
			out = append(out, dominant)
		}
	}

	fields = out
	switch sorter {
	case KeySortMode_Default:
		sort.Sort(StructMapEntry_byFieldRoute(fields))
	case KeySortMode_Strings:
		//sort.Sort(StructMapEntry_byName(fields))
		// it's already in this order, though, so, pass
	case KeySortMode_RFC7049:
		sort.Sort(StructMapEntry_RFC7049(fields))
	default:
		panic("invalid struct sorter option")
	}

	return fields
}

// If the first character of the string is uppercase, return a string
// where it is switched to lowercase.
// We use this to make go field names look more like what everyone else
// in the universe expects their json to look like by default: snakeCase.
func downcaseFirstLetter(s string) string {
	if s == "" {
		return ""
	}
	r := rune(s[0]) // if multibyte chars: you're left alone.
	if !unicode.IsUpper(r) {
		return s
	}
	return string(unicode.ToLower(r)) + s[1:]
}

// dominantField looks through the fields, all of which are known to
// have the same name, to find the single field that dominates the
// others using Go's embedding rules, modified by the presence of
// JSON tags. If there are multiple top-level fields, the boolean
// will be false: This condition is an error in Go and we skip all
// the fields.
func dominantField(fields []StructMapEntry) (StructMapEntry, bool) {
	// The fields are sorted in increasing index-length order. The winner
	// must therefore be one with the shortest index length. Drop all
	// longer entries, which is easy: just truncate the slice.
	length := len(fields[0].ReflectRoute)
	tagged := -1 // Index of first tagged field.
	for i, f := range fields {
		if len(f.ReflectRoute) > length {
			fields = fields[:i]
			break
		}
		if f.tagged {
			if tagged >= 0 {
				// Multiple tagged fields at the same level: conflict.
				// Return no field.
				return StructMapEntry{}, false
			}
			tagged = i
		}
	}
	if tagged >= 0 {
		return fields[tagged], true
	}
	// All remaining fields have the same length. If there's more than one,
	// we have a conflict (two fields named "X" at the same level) and we
	// return no field.
	if len(fields) > 1 {
		return StructMapEntry{}, false
	}
	return fields[0], true
}

// StructMapEntry_byName sorts field by name,
// breaking ties with depth,
// then breaking ties with "name came from tag",
// then breaking ties with FieldRoute sequence.
type StructMapEntry_byName []StructMapEntry

func (x StructMapEntry_byName) Len() int      { return len(x) }
func (x StructMapEntry_byName) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x StructMapEntry_byName) Less(i, j int) bool {
	if x[i].SerialName != x[j].SerialName {
		return x[i].SerialName < x[j].SerialName
	}
	if len(x[i].ReflectRoute) != len(x[j].ReflectRoute) {
		return len(x[i].ReflectRoute) < len(x[j].ReflectRoute)
	}
	if x[i].tagged != x[j].tagged {
		return x[i].tagged
	}
	return StructMapEntry_byFieldRoute(x).Less(i, j)
}

// StructMapEntry_RFC7049 sorts fields as specified in RFC7049,
type StructMapEntry_RFC7049 []StructMapEntry

func (x StructMapEntry_RFC7049) Len() int      { return len(x) }
func (x StructMapEntry_RFC7049) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x StructMapEntry_RFC7049) Less(i, j int) bool {
	il, jl := len(x[i].SerialName), len(x[j].SerialName)
	switch {
	case il < jl:
		return true
	case il > jl:
		return false
	default:
		return x[i].SerialName < x[j].SerialName
	}
}

// StructMapEntry_byFieldRoute sorts field by FieldRoute sequence
// (e.g., roughly source declaration order within each type).
type StructMapEntry_byFieldRoute []StructMapEntry

func (x StructMapEntry_byFieldRoute) Len() int      { return len(x) }
func (x StructMapEntry_byFieldRoute) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x StructMapEntry_byFieldRoute) Less(i, j int) bool {
	for k, xik := range x[i].ReflectRoute {
		if k >= len(x[j].ReflectRoute) {
			return false
		}
		if xik != x[j].ReflectRoute[k] {
			return xik < x[j].ReflectRoute[k]
		}
	}
	return len(x[i].ReflectRoute) < len(x[j].ReflectRoute)
}

// tagOptions is the string following a comma in a struct field's
// tag, or the empty string. It does not include the leading comma.
type tagOptions string

// parseTag splits a struct field's tag into its name and
// comma-separated options.
func parseTag(tag string) (string, tagOptions) {
	if idx := strings.Index(tag, ","); idx != -1 {
		return tag[:idx], tagOptions(tag[idx+1:])
	}
	return tag, tagOptions("")
}

// Contains reports whether a comma-separated list of options
// contains a particular substr flag. substr must be surrounded by a
// string boundary or commas.
func (o tagOptions) Contains(optionName string) bool {
	if len(o) == 0 {
		return false
	}
	s := string(o)
	for s != "" {
		var next string
		i := strings.Index(s, ",")
		if i >= 0 {
			s, next = s[:i], s[i+1:]
		}
		if s == optionName {
			return true
		}
		s = next
	}
	return false
}

func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:<=>?@[]^_{|}~ ", c):
			// Backslash and quote chars are reserved, but
			// otherwise any punctuation chars are allowed
			// in a tag name.
		default:
			if !unicode.IsLetter(c) && !unicode.IsDigit(c) {
				return false
			}
		}
	}
	return true
}
