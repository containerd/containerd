/*
Copyright The Kubernetes Authors.

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

package strictjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"sigs.k8s.io/security-profiles-merger/spm"
)

// PathsError wraps kind with the given field paths, or returns nil when
// there are none.
func PathsError(kind error, paths []string, omitted int) error {
	if len(paths) == 0 {
		return nil
	}

	return fieldPathsError(kind, paths, omitted)
}

// InvalidUTF8 reports the first byte of raw that does not start a valid
// UTF-8 sequence, or the first \u escape that spells half a surrogate pair,
// or nil when there is neither. encoding/json replaces both with U+FFFD, so
// two profiles whose syscall names differ only in them decode to the same
// name and merge into one rule; in an artifact, a name spelled that way is a
// sign the bytes were crafted.
func InvalidUTF8(raw []byte) error {
	if utf8.Valid(raw) {
		offset := loneSurrogateEscape(raw)
		if offset < 0 {
			return nil
		}

		return fmt.Errorf("%w (lone surrogate escape at byte %d)", spm.ErrInvalidUTF8, offset)
	}

	offset := 0

	for offset < len(raw) {
		_, size := utf8.DecodeRune(raw[offset:])
		if size == 1 && raw[offset] >= utf8.RuneSelf {
			break
		}

		offset += size
	}

	return fmt.Errorf("%w (first at byte %d)", spm.ErrInvalidUTF8, offset)
}

// loneSurrogateEscape returns the offset of the first \u escape in raw that
// names a surrogate without its other half, or -1. A backslash only occurs
// inside a string in valid JSON, and raw that is not valid JSON is refused
// by the decoder, so the scan does not track strings.
func loneSurrogateEscape(raw []byte) int {
	for offset := 0; offset < len(raw); offset++ {
		if raw[offset] != '\\' {
			continue
		}

		char, isEscape := unicodeEscape(raw[offset:])
		if !isEscape {
			// Skip the escaped byte, which may be a backslash itself.
			offset++

			continue
		}

		low, lowIsEscape := unicodeEscape(raw[min(offset+unicodeEscapeLen, len(raw)):])

		switch {
		case !utf16.IsSurrogate(char):
			offset += unicodeEscapeLen - 1
		case lowIsEscape && utf16.DecodeRune(char, low) != unicode.ReplacementChar:
			offset += unicodeEscapeLen + unicodeEscapeLen - 1
		default:
			return offset
		}
	}

	return -1
}

// unicodeEscapeLen is the length of a \uXXXX escape.
const unicodeEscapeLen = 6

// unicodeEscape returns the code unit a \uXXXX escape at the start of raw
// names.
func unicodeEscape(raw []byte) (rune, bool) {
	if len(raw) < unicodeEscapeLen || raw[0] != '\\' || raw[1] != 'u' {
		return 0, false
	}

	value, err := strconv.ParseUint(string(raw[2:unicodeEscapeLen]), 16, 16)
	if err != nil {
		return 0, false
	}

	return rune(value), true
}

// UnknownFieldsOf reports the members of raw that T has no field for.
//
// Enumerating them needs the document decoded into interface values, which
// costs more memory than the profile itself, so a strict decode runs first
// to learn whether there is anything to report. The caller has already
// decoded raw into a T, so the only thing a strict decode can still object
// to is an unknown member.
func UnknownFieldsOf[T any](raw []byte) ([]string, int) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(new(T))
	if err == nil {
		return nil, 0
	}

	paths, omitted := UnknownFields(raw, reflect.TypeFor[T]())

	// The walk matches members to fields the way encoding/json does. Should
	// the two ever disagree, the decoder's own finding is reported rather
	// than dropped, so that the check fails closed. A document that is not
	// valid JSON is the decoder's to refuse, and says nothing here.
	name, unknown := decoderFieldName(err)
	if len(paths) == 0 && unknown && json.Valid(raw) {
		return []string{name}, 0
	}

	return paths, omitted
}

// decoderFieldName returns the member name out of encoding/json's unknown
// field error, and whether err is one.
func decoderFieldName(err error) (string, bool) {
	quoted, found := strings.CutPrefix(err.Error(), "json: unknown field ")
	if !found {
		return "", false
	}

	name, unquoteErr := strconv.Unquote(quoted)
	if unquoteErr != nil {
		return quoted, true
	}

	return name, true
}

// UnknownFields returns the members of a JSON document that the target type
// has no field for, as paths such as "syscalls[0].arg", in document order
// with object keys sorted. encoding/json stops at the first unknown member
// when asked to reject them, so the document is walked here instead to
// report every one. Members are matched to fields the way encoding/json
// does: by the exact JSON name first, case-insensitively otherwise.
func UnknownFields(raw []byte, target reflect.Type) ([]string, int) {
	found := walkFields(raw, target)

	return found.unknown.paths, found.unknown.omitted
}

// MisspelledFieldsOf reports the members of raw whose name matches a field
// of T only ignoring case, such as "Syscalls" or "\u017fyscalls" for
// "syscalls". encoding/json fills the field from such a member, while a
// reader that compares names exactly, as a C runtime does, drops it: the
// two read different rules from one document. Neither DisallowUnknownFields
// nor UnknownFieldsOf objects to one, since the decoder knows the field.
//
// Enumerating them costs what UnknownFieldsOf's walk costs, so a scan of the
// member names runs first and the walk only follows when a name could be a
// misspelling of some field of T.
func MisspelledFieldsOf[T any](raw []byte) ([]string, int) {
	target := reflect.TypeFor[T]()
	if !mayMisspell(raw, fieldSpellings(target)) {
		return nil, 0
	}

	found := walkFields(raw, target)

	return found.misspelled.paths, found.misspelled.omitted
}

// The struct tag `strict:"required"` marks a field a document has to hold.
// It is for a field whose zero value is a value of its own, so that a member
// left out cannot be told from one that names it.
const (
	requiredTagKey   = "strict"
	requiredTagValue = "required"
)

// MissingFieldsOf reports the required fields of T that raw names no value
// for: the member is left out or null, or the object that would hold it is.
// encoding/json decodes the zero value in each case. decoded is what raw
// decoded into.
//
// A field is reported only where decoded holds its zero value. A member the
// document repeats is read as its last occurrence here, while the decoder
// keeps an earlier value when the last one is null, so the decoded value is
// what says whether the document named one.
//
// The walk decodes the document a second time, into interface values, so it
// only runs for a type that has required fields at all and a document that
// left one of them zero.
func MissingFieldsOf[T any](raw []byte, decoded *T) ([]string, int) {
	value := reflect.ValueOf(decoded)
	if !requiresFields(reflect.TypeFor[T]()) || !holdsZeroRequired(value) {
		return nil, 0
	}

	var (
		document any
		found    pathCollector
	)

	err := json.Unmarshal(raw, &document)
	if err != nil {
		return nil, 0
	}

	walkMissing(document, value, "", &found)

	return found.paths, found.omitted
}

// requiredTypes caches requiresFields by type.
//
//nolint:gochecknoglobals // a cache of immutable values keyed by type
var requiredTypes sync.Map

// requiresFields reports whether a struct type reachable from target has a
// required field, embedded structs included.
func requiresFields(target reflect.Type) bool {
	if cached, ok := requiredTypes.Load(target); ok {
		required, _ := cached.(bool)

		return required
	}

	required := false
	seen := map[reflect.Type]bool{}
	pending := []reflect.Type{target}

	for len(pending) > 0 && !required {
		typ := elementType(pending[len(pending)-1])
		pending = pending[:len(pending)-1]

		if typ.Kind() != reflect.Struct || seen[typ] {
			continue
		}

		seen[typ] = true

		for idx := range typ.NumField() {
			field := typ.Field(idx)
			required = required || isRequired(field.Tag)
			pending = append(pending, field.Type)
		}
	}

	requiredTypes.Store(target, required)

	return required
}

// holdsZeroRequired reports whether a decoded value holds the zero value in
// a required field, at any depth.
func holdsZeroRequired(value reflect.Value) bool {
	kind := value.Kind()

	if kind == reflect.Pointer || kind == reflect.Interface {
		return !value.IsNil() && holdsZeroRequired(value.Elem())
	}

	if kind == reflect.Slice || kind == reflect.Array {
		for idx := range value.Len() {
			if holdsZeroRequired(value.Index(idx)) {
				return true
			}
		}

		return false
	}

	if kind == reflect.Map {
		return slices.ContainsFunc(value.MapKeys(), func(key reflect.Value) bool {
			return holdsZeroRequired(value.MapIndex(key))
		})
	}

	return kind == reflect.Struct && structHoldsZeroRequired(value)
}

// structHoldsZeroRequired is holdsZeroRequired for a struct.
func structHoldsZeroRequired(value reflect.Value) bool {
	for idx := range value.NumField() {
		// An embedded struct holds exported fields the decoder fills even
		// where its own type is not exported.
		field := value.Type().Field(idx)
		if !field.IsExported() && !field.Anonymous {
			continue
		}

		if isRequired(field.Tag) && value.Field(idx).IsZero() ||
			holdsZeroRequired(value.Field(idx)) {
			return true
		}
	}

	return false
}

// isRequired reports whether the tag of a struct field marks it required.
func isRequired(tag reflect.StructTag) bool {
	return tag.Get(requiredTagKey) == requiredTagValue
}

// walkMissing walks a decoded value next to the document it was decoded
// from and collects the required fields the document names no value for.
// value is nil where the document holds null or nothing for decoded, which
// leaves every member of a struct there unnamed.
func walkMissing(value any, decoded reflect.Value, prefix string, found *pathCollector) {
	kind := decoded.Kind()

	if kind == reflect.Pointer || kind == reflect.Interface {
		if !decoded.IsNil() {
			walkMissing(value, decoded.Elem(), prefix, found)
		}

		return
	}

	if kind == reflect.Slice || kind == reflect.Array {
		walkMissingItems(value, decoded, prefix, found)
	}

	if kind == reflect.Map {
		walkMissingValues(value, decoded, prefix, found)
	}

	if kind == reflect.Struct {
		object, _ := value.(map[string]any)
		walkMissingFields(object, decoded, prefix, found)
	}
}

// walkMissingItems is walkMissing for a slice or an array.
func walkMissingItems(value any, decoded reflect.Value, prefix string, found *pathCollector) {
	items, _ := value.([]any)

	for idx := range decoded.Len() {
		var item any
		if idx < len(items) {
			item = items[idx]
		}

		walkMissing(item, decoded.Index(idx), prefix+"["+strconv.Itoa(idx)+"]", found)
	}
}

// walkMissingValues is walkMissing for a map with string keys, the only
// kind a document's members decode into.
func walkMissingValues(value any, decoded reflect.Value, prefix string, found *pathCollector) {
	if decoded.Type().Key().Kind() != reflect.String {
		return
	}

	object, _ := value.(map[string]any)

	for _, key := range slices.Sorted(maps.Keys(object)) {
		held := decoded.MapIndex(reflect.ValueOf(key).Convert(decoded.Type().Key()))
		if held.IsValid() {
			walkMissing(object[key], held, joinFieldPath(prefix, key), found)
		}
	}
}

// walkMissingFields is walkMissing for a struct, whose members object
// holds. A struct embedded in it reads its members from the same object.
func walkMissingFields(
	object map[string]any, decoded reflect.Value, prefix string, found *pathCollector,
) {
	for idx := range decoded.NumField() {
		field := decoded.Type().Field(idx)

		name, embedded, visible := memberName(&field)
		if !visible {
			continue
		}

		if embedded {
			walkMissing(object, decoded.Field(idx), prefix, found)

			continue
		}

		member := memberOf(object, name)
		path := joinFieldPath(prefix, name)

		if isRequired(field.Tag) && member == nil && decoded.Field(idx).IsZero() {
			found.add(path)
		}

		walkMissing(member, decoded.Field(idx), path, found)
	}
}

// memberName returns the name of the member that fills a struct field, as
// jsonFields reads it: whether the field is an embedded struct, whose
// members sit in the object of the struct embedding it, and whether the
// decoder sees the field at all.
func memberName(field *reflect.StructField) (string, bool, bool) {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")

	switch {
	case name == "-" || !field.IsExported() && !field.Anonymous:
		return "", false, false
	case field.Anonymous && name == "":
		return "", true, true
	case name == "":
		return field.Name, false, true
	default:
		return name, false, true
	}
}

// memberOf returns the value of the member of an object that fills the
// named field, matched as encoding/json matches it, or nil when the object
// holds none or holds null for it.
func memberOf(object map[string]any, name string) any {
	if value, ok := object[name]; ok {
		return value
	}

	folded := foldName(name)

	for key, value := range object {
		if foldName(key) == folded {
			return value
		}
	}

	return nil
}

// HasField reports whether a member of the given name fills a field of the
// struct type target, matched the way encoding/json matches it: exactly or
// ignoring case.
func HasField(target reflect.Type, name string) bool {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}

	if target.Kind() != reflect.Struct {
		return false
	}

	_, known, _ := fieldsOf(target).lookup(name)

	return known
}

// fieldFindings are the members a walk of a document found wanting.
type fieldFindings struct {
	// unknown holds the members no field reads.
	unknown pathCollector
	// misspelled holds the members a field reads only ignoring case.
	misspelled pathCollector
}

// walkFields walks a document against the target type. A document that is
// not valid JSON yields no findings; it is the decoder's to refuse.
func walkFields(raw []byte, target reflect.Type) *fieldFindings {
	var (
		document any
		found    fieldFindings
	)

	err := json.Unmarshal(raw, &document)
	if err != nil {
		return &found
	}

	walkUnknownFields(document, target, "", &found)

	return &found
}

func walkUnknownFields(value any, typ reflect.Type, prefix string, found *fieldFindings) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	kind := typ.Kind()

	if kind == reflect.Struct {
		walkStructFields(value, typ, prefix, found)
	}

	if kind == reflect.Slice || kind == reflect.Array {
		walkSliceItems(value, typ, prefix, found)
	}

	if kind == reflect.Map {
		walkMapValues(value, typ, prefix, found)
	}
}

func walkSliceItems(value any, typ reflect.Type, prefix string, found *fieldFindings) {
	// []byte and json.RawMessage take any JSON value.
	if typ.Elem().Kind() == reflect.Uint8 {
		return
	}

	items, ok := value.([]any)
	if !ok {
		return
	}

	for idx, item := range items {
		walkUnknownFields(item, typ.Elem(), prefix+"["+strconv.Itoa(idx)+"]", found)
	}
}

func walkMapValues(value any, typ reflect.Type, prefix string, found *fieldFindings) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}

	for _, key := range slices.Sorted(maps.Keys(object)) {
		walkUnknownFields(object[key], typ.Elem(), joinFieldPath(prefix, key), found)
	}
}

func walkStructFields(value any, typ reflect.Type, prefix string, found *fieldFindings) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}

	fields := fieldsOf(typ)

	for _, key := range slices.Sorted(maps.Keys(object)) {
		fieldType, known, exact := fields.lookup(key)
		if !known {
			found.unknown.addLazily(func() string { return joinFieldPath(prefix, key) })

			continue
		}

		// The decoder reads the member, so what it holds is walked like
		// any other; the name itself is what another reader drops.
		if !exact {
			found.misspelled.add(joinFieldPath(prefix, key))
		}

		walkUnknownFields(object[key], fieldType, joinFieldPath(prefix, key), found)
	}
}

// fieldSpelling is how the fields of a type graph spell one folded name.
type fieldSpelling struct {
	// name is the spelling of the first field found with the folded name.
	name string
	// several reports whether fields of different structs spell it
	// differently, in which case any member with the folded name may be a
	// misspelling of one of them.
	several bool
}

// fieldSpellings maps the folded name of every field of every struct type
// reachable from target to how those fields spell it.
func fieldSpellings(target reflect.Type) map[string]fieldSpelling {
	spellings := map[string]fieldSpelling{}
	seen := map[reflect.Type]bool{}
	pending := []reflect.Type{target}

	for len(pending) > 0 {
		typ := elementType(pending[len(pending)-1])
		pending = pending[:len(pending)-1]

		if typ.Kind() != reflect.Struct || seen[typ] {
			continue
		}

		seen[typ] = true

		for name, fieldType := range jsonFields(typ).exact {
			addSpelling(spellings, name)

			pending = append(pending, fieldType)
		}
	}

	return spellings
}

// elementType returns the type a value of typ holds its members in: the
// type itself, or the element type of a pointer, slice, array or map.
func elementType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice ||
		typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}

	return typ
}

// addSpelling records how a field spells its folded name.
func addSpelling(spellings map[string]fieldSpelling, name string) {
	folded := foldName(name)

	spelling, exists := spellings[folded]
	if !exists {
		spellings[folded] = fieldSpelling{name: name, several: false}
	} else if spelling.name != name {
		spellings[folded] = fieldSpelling{name: spelling.name, several: true}
	}
}

// mayMisspell reports whether raw holds a string that folds to the name of
// a field but is not spelled the way every such field spells it. A member
// the walk would report as misspelled is such a string, so a document
// without one needs no walk. String values are scanned as well as member
// names, which only costs a walk the document did not need.
func mayMisspell(raw []byte, spellings map[string]fieldSpelling) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}

		text, isString := token.(string)
		if !isString {
			continue
		}

		spelling, exists := spellings[foldName(text)]
		if exists && (spelling.several || spelling.name != text) {
			return true
		}
	}
}

// joinFieldPath appends a member name to a field path. A name that cannot
// be spelled as a plain path segment is bracketed and quoted, the way an
// array index is, so that one path names one member: written with a dot
// unconditionally, the member "a.b" and the member "b" of the object "a"
// spell the same path, and a member named "" spells the path of the object
// holding it. Every member of a profile document is a plain name, so this
// only shows up for a document that is not one.
func joinFieldPath(prefix, key string) string {
	if !plainFieldName(key) {
		return prefix + "[" + strconv.Quote(key) + "]"
	}

	if prefix == "" {
		return key
	}

	return prefix + "." + key
}

// plainFieldName reports whether a member name can be a path segment as it
// is: a non-empty, valid UTF-8 name holding none of the punctuation a path
// is built from and nothing unprintable. A name outside that, which no
// profile document has, is bracketed and quoted instead, which also spells
// out a byte a terminal would otherwise swallow.
func plainFieldName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}

	for _, char := range name {
		switch {
		case char == '.', char == '[', char == ']', char == '"', char == '\\':
			return false
		case unicode.IsControl(char):
			return false
		}
	}

	return true
}

// fieldSet maps the JSON names of a struct's fields to their types, once by
// exact name and once folded the way encoding/json folds a name for the
// fallback match. That folding is not strings.ToLower: "\u0130" lowers to
// "i" but folds to itself, so a lowered lookup takes a member the decoder
// drops for a known one.
type fieldSet struct {
	exact  map[string]reflect.Type
	folded map[string]reflect.Type
}

// lookup returns the type of the field a member fills, whether one does,
// and whether the member names it exactly rather than only ignoring case.
func (set fieldSet) lookup(key string) (reflect.Type, bool, bool) {
	if fieldType, ok := set.exact[key]; ok {
		return fieldType, true, true
	}

	fieldType, ok := set.folded[foldName(key)]

	return fieldType, ok, false
}

// fieldSets holds the field set of every struct type asked for, by type. A
// walk asks once per object of a document and type detection once per
// member, and a document chooses how many of both it has, while the types
// are the handful a profile is made of.
//
//nolint:gochecknoglobals // a cache of immutable values keyed by type
var fieldSets sync.Map

// fieldsOf returns jsonFields of a struct type, computed once per type.
func fieldsOf(typ reflect.Type) fieldSet {
	if cached, ok := fieldSets.Load(typ); ok {
		set, _ := cached.(fieldSet)

		return set
	}

	set := jsonFields(typ)
	fieldSets.Store(typ, set)

	return set
}

// jsonFields collects the JSON-visible fields of a struct type, including
// those promoted from embedded structs. As in encoding/json, a field of the
// struct itself wins over a promoted field of the same name, so promoted
// fields are added last.
func jsonFields(typ reflect.Type) fieldSet {
	set := fieldSet{exact: map[string]reflect.Type{}, folded: map[string]reflect.Type{}}

	var embedded []reflect.Type

	for idx := range typ.NumField() {
		field := typ.Field(idx)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")

		if name == "-" || !field.IsExported() && !field.Anonymous {
			continue
		}

		if field.Anonymous && name == "" {
			embedded = append(embedded, field.Type)

			continue
		}

		if name == "" {
			name = field.Name
		}

		set.add(name, field.Type)
	}

	for _, embeddedType := range embedded {
		set.addPromoted(embeddedType)
	}

	return set
}

func (set fieldSet) add(name string, fieldType reflect.Type) {
	if _, exists := set.exact[name]; exists {
		return
	}

	set.exact[name] = fieldType
	set.folded[foldName(name)] = fieldType
}

func (set fieldSet) addPromoted(embedded reflect.Type) {
	for embedded.Kind() == reflect.Pointer {
		embedded = embedded.Elem()
	}

	if embedded.Kind() != reflect.Struct {
		return
	}

	for name, fieldType := range jsonFields(embedded).exact {
		set.add(name, fieldType)
	}
}
