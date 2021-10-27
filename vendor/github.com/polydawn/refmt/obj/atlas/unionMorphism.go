package atlas

import (
	"fmt"
	"reflect"
	"sort"
)

type UnionKeyedMorphism struct {
	// Mapping of typehint key strings to atlasEntry that should be delegated to.
	Elements map[string]*AtlasEntry
	// Mapping of rtid to string (roughly the dual of the Elements map).
	Mappings map[uintptr]string
	// Purely to have in readiness for error messaging.
	KnownMembers []string
}

func (x *BuilderCore) KeyedUnion() *BuilderUnionKeyedMorphism {
	if x.entry.Type.Kind() != reflect.Interface {
		panic(fmt.Errorf("cannot use union morphisms for type %q, which is kind %s", x.entry.Type, x.entry.Type.Kind()))
	}
	x.entry.UnionKeyedMorphism = &UnionKeyedMorphism{
		Elements: make(map[string]*AtlasEntry),
		Mappings: make(map[uintptr]string),
	}
	return &BuilderUnionKeyedMorphism{x.entry}
}

type BuilderUnionKeyedMorphism struct {
	entry *AtlasEntry
}

func (x *BuilderUnionKeyedMorphism) Of(elements map[string]*AtlasEntry) *AtlasEntry {
	cfg := x.entry.UnionKeyedMorphism
	for hint, ent := range elements {
		// FIXME: check that all the delegates are... well struct or map machines really, but definitely blacklisting other delegating machinery.
		// FIXME: and sanity check that they can all be assigned to the interface ffs.

		cfg.Elements[hint] = ent
		cfg.Mappings[reflect.ValueOf(ent.Type).Pointer()] = hint
		cfg.KnownMembers = append(cfg.KnownMembers, hint)
	}
	sort.Strings(cfg.KnownMembers)
	return x.entry
}
