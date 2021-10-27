package atlas

import (
	"fmt"
	"reflect"
)

type Atlas struct {
	// Map typeinfo to a static description of how that type should be handled.
	// (The internal machinery that will wield this information, and has memory of
	// progress as it does so, is configured using the AtlasEntry, but allocated separately.
	// The machinery is stateful and mutable; the AtlasEntry is not.)
	//
	// We use 'var rtid uintptr = reflect.ValueOf(rt).Pointer()' -- pointer of the
	// value of the reflect.Type info -- as an index.
	// This is both unique and correctly converges when recomputed, and much
	// faster to compare against than reflect.Type (which is an interface that
	// tends to contain fairly large structures).
	mappings map[uintptr]*AtlasEntry

	// Mapping of tag ints to atlasEntry for quick lookups when the
	// unmarshaller hits a tag.  Values are a subset of `mappings`.
	tagMappings map[int]*AtlasEntry

	// MapMorphism specifies the default map sorting scheme
	defaultMapMorphism *MapMorphism
}

func Build(entries ...*AtlasEntry) (Atlas, error) {
	atl := Atlas{
		mappings:           make(map[uintptr]*AtlasEntry),
		tagMappings:        make(map[int]*AtlasEntry),
		defaultMapMorphism: &MapMorphism{KeySortMode_Default},
	}
	for _, entry := range entries {
		rtid := reflect.ValueOf(entry.Type).Pointer()
		if _, exists := atl.mappings[rtid]; exists {
			return Atlas{}, fmt.Errorf("repeated entry for type %v", entry.Type)
		}
		atl.mappings[rtid] = entry

		if entry.Tagged == true {
			if prev, exists := atl.tagMappings[entry.Tag]; exists {
				return Atlas{}, fmt.Errorf("repeated tag %v on type %v (already mapped to type %v)", entry.Tag, entry.Type, prev.Type)
			}
			atl.tagMappings[entry.Tag] = entry
		}
	}
	return atl, nil
}
func MustBuild(entries ...*AtlasEntry) Atlas {
	atl, err := Build(entries...)
	if err != nil {
		panic(err)
	}
	return atl
}

func (atl Atlas) WithMapMorphism(m MapMorphism) Atlas {
	atl.defaultMapMorphism = &m
	return atl
}

// Gets the AtlasEntry for a typeID.  Used by obj package, not meant for user facing.
func (atl Atlas) Get(rtid uintptr) (*AtlasEntry, bool) {
	ent, ok := atl.mappings[rtid]
	return ent, ok
}

// Gets the AtlasEntry for a tag int.  Used by obj package, not meant for user facing.
func (atl Atlas) GetEntryByTag(tag int) (*AtlasEntry, bool) {
	ent, ok := atl.tagMappings[tag]
	return ent, ok
}

// Gets the default map morphism config.  Used by obj package, not meant for user facing.
func (atl Atlas) GetDefaultMapMorphism() *MapMorphism {
	return atl.defaultMapMorphism
}
