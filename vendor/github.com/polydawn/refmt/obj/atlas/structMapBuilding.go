package atlas

import (
	"fmt"
	"reflect"
	"strings"
)

func (x *BuilderCore) StructMap() *BuilderStructMap {
	if x.entry.Type.Kind() != reflect.Struct {
		panic(fmt.Errorf("cannot use structMap for type %q, which is kind %s", x.entry.Type, x.entry.Type.Kind()))
	}
	x.entry.StructMap = &StructMap{}
	return &BuilderStructMap{x.entry}
}

type BuilderStructMap struct {
	entry *AtlasEntry
}

func (x *BuilderStructMap) Complete() *AtlasEntry {
	return x.entry
}

/*
	Add a field to the mapping based on its name.

	Given a struct:

		struct{
			X int
			Y struct{ Z int }
		}

	`AddField("X", {"x", ...}) will cause that field to be serialized as key "x";
	`AddField("Y.Z", {"z", ...})` will cause that *nested* field to be serialized
	as key "z" in the same object (e.g. "x" and "z" will be siblings).

	Returns the mutated builder for convenient call chaining.

	If the fieldName string doesn't map onto the structure type info,
	a panic will be raised.
*/
func (x *BuilderStructMap) AddField(fieldName string, mapping StructMapEntry) *BuilderStructMap {
	fieldNameSplit := strings.Split(fieldName, ".")
	rr, rt, err := fieldNameToReflectRoute(x.entry.Type, fieldNameSplit)
	if err != nil {
		panic(err) // REVIEW: now that we have the builder obj, we could just curry these into it until 'Complete' is called (or, thus, 'MustComplete'!).
	}
	mapping.ReflectRoute = rr
	mapping.Type = rt
	x.entry.StructMap.Fields = append(x.entry.StructMap.Fields, mapping)
	return x
}

func (x *BuilderStructMap) IgnoreKey(serialKeyName string) *BuilderStructMap {
	x.entry.StructMap.Fields = append(x.entry.StructMap.Fields, StructMapEntry{
		SerialName: serialKeyName,
		Ignore:     true,
	})
	return x
}

func fieldNameToReflectRoute(rt reflect.Type, fieldNameSplit []string) (rr ReflectRoute, _ reflect.Type, _ error) {
	for _, fn := range fieldNameSplit {
		rf, ok := rt.FieldByName(fn)
		if !ok {
			return nil, nil, ErrStructureMismatch{rt.Name(), "does not have field named " + fn}
		}
		rr = append(rr, rf.Index...)
		rt = rf.Type
	}
	return rr, rt, nil
}

/*
	Automatically generate mappings by looking at the struct type info,
	taking any hints from tags, and appending that to the builder.

	You may use autogeneration in concert with manually adding field mappings,
	though if doing so be mindful not to map the same fields twice.
*/
func (x *BuilderStructMap) Autogenerate() *BuilderStructMap {
	autoEntry := AutogenerateStructMapEntry(x.entry.Type)
	x.entry.StructMap.Fields = append(x.entry.StructMap.Fields, autoEntry.StructMap.Fields...)
	return x
}

/*
	Automatically generate mappings using a given struct field sorting scheme
*/
func (x *BuilderStructMap) AutogenerateWithSortingScheme(sorting KeySortMode) *BuilderStructMap {
	autoEntry := AutogenerateStructMapEntryUsingTags(x.entry.Type, "refmt", sorting)
	x.entry.StructMap.Fields = append(x.entry.StructMap.Fields, autoEntry.StructMap.Fields...)
	return x
}
