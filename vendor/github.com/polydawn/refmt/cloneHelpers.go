package refmt

import (
	"github.com/polydawn/refmt/obj"
	"github.com/polydawn/refmt/obj/atlas"
	"github.com/polydawn/refmt/shared"
)

func Clone(src, dst interface{}) error {
	return CloneAtlased(src, dst, atlas.MustBuild())
}
func MustClone(src, dst interface{}) {
	if err := Clone(src, dst); err != nil {
		panic(err)
	}
}

func CloneAtlased(src, dst interface{}, atl atlas.Atlas) error {
	return NewCloner(atl).Clone(src, dst)
}
func MustCloneAtlased(src, dst interface{}, atl atlas.Atlas) {
	if err := CloneAtlased(src, dst, atl); err != nil {
		panic(err)
	}
}

type Cloner interface {
	Clone(src, dst interface{}) error
}

func NewCloner(atl atlas.Atlas) Cloner {
	x := &cloner{
		marshaller:   obj.NewMarshaller(atl),
		unmarshaller: obj.NewUnmarshaller(atl),
	}
	x.pump = shared.TokenPump{x.marshaller, x.unmarshaller}
	return x
}

type cloner struct {
	marshaller   *obj.Marshaller
	unmarshaller *obj.Unmarshaller
	pump         shared.TokenPump
}

func (c cloner) Clone(src, dst interface{}) error {
	c.marshaller.Bind(src)
	c.unmarshaller.Bind(dst)
	return c.pump.Run()
}
