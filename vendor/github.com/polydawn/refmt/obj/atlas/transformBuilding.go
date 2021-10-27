package atlas

import (
	"reflect"
)

func (x *BuilderCore) Transform() *BuilderTransform {
	// no checks on x.entry.Type.Kind() here -- transforms can be pretty much any<->any
	return &BuilderTransform{x.entry}
}

type BuilderTransform struct {
	entry *AtlasEntry
}

func (x *BuilderTransform) Complete() *AtlasEntry {
	return x.entry
}

func (x *BuilderTransform) TransformMarshal(trFunc MarshalTransformFunc, toType reflect.Type) *BuilderTransform {
	x.entry.MarshalTransformFunc = trFunc
	x.entry.MarshalTransformTargetType = toType
	return x
}

func (x *BuilderTransform) TransformUnmarshal(trFunc UnmarshalTransformFunc, toType reflect.Type) *BuilderTransform {
	x.entry.UnmarshalTransformFunc = trFunc
	x.entry.UnmarshalTransformTargetType = toType
	return x
}
