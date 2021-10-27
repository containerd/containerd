package atlas

import (
	"fmt"
	"reflect"
)

type MapMorphism struct {
	KeySortMode KeySortMode
}

func (x *BuilderCore) MapMorphism() *BuilderMapMorphism {
	if x.entry.Type.Kind() != reflect.Map {
		panic(fmt.Errorf("cannot use mapMorphism for type %q, which is kind %s", x.entry.Type, x.entry.Type.Kind()))
	}
	x.entry.MapMorphism = &MapMorphism{
		KeySortMode_Default,
	}
	return &BuilderMapMorphism{x.entry}
}

type BuilderMapMorphism struct {
	entry *AtlasEntry
}

func (x *BuilderMapMorphism) Complete() *AtlasEntry {
	return x.entry
}

func (x *BuilderMapMorphism) SetKeySortMode(km KeySortMode) *BuilderMapMorphism {
	switch km {
	case KeySortMode_Default, KeySortMode_Strings, KeySortMode_RFC7049:
		x.entry.MapMorphism.KeySortMode = km
	default:
		panic(fmt.Errorf("invalid key sort mode %q", km))
	}
	return x
}
