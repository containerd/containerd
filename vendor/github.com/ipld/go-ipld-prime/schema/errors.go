package schema

import (
	"fmt"

	"github.com/ipld/go-ipld-prime"
)

// TODO: errors in this package remain somewhat slapdash.
//
//  - ipld.ErrUnmatchable is used as a catch-all in some places, and contains who-knows-what values wrapped in the Reason field.
//    - sometimes this wraps things like strconv errors... and on the one hand, i'm kinda okay with that; on the other, maybe saying a bit more with types before getting to that kind of shrug would be nice.
//  - we probably want to use `Type` values, right?
//    - or do we: because then we probably need a `Repr bool` next to it, or lots of messages would be nonsensical.
//    - this is *currently* problematic because we don't actually generate type info consts yet.  Hopefully soon; but the pain, meanwhile, is... substantial.
//      - "substantial" is an understatement.  it makes incremental development almost impossible because stringifying error reports turn into nil pointer crashes!
//    - other ipld-wide errors like `ipld.ErrWrongKind` *sometimes* refer to a TypeName... but don't *have* to, because they also arise at the merely-datamodel level; what would we do with these?
//      - it's undesirable (not to mention intensely forbidden for import cycle reasons) for those error types to refer to schema.Type.
//        - if we must have TypeName treated stringily in some cases, is it really useful to use full type info in other cases -- inconsistently?
//    - regardless of where we end up with this, some sort of an embed for helping deal with munging and printing this would probably be wise.
//  - generally, whether you should expect an "ipld.Err*" or a "schema.Err*" from various methods is quite unclear.
//  - it's possible that we should wrap *all* schema-level errors in a single "ipld.ErrSchemaNoMatch" error of some kind, to fix the above.  as yet undecided.

// ErrNoSuchField may be returned from lookup functions on the Node
// interface when a field is requested which doesn't exist,
// or from assigning data into on a MapAssembler for a struct
// when the key doesn't match a field name in the structure
// (or, when assigning data into a ListAssembler and the list size has
// reached out of bounds, in case of a struct with list-like representations!).
type ErrNoSuchField struct {
	Type Type

	Field ipld.PathSegment
}

func (e ErrNoSuchField) Error() string {
	if e.Type == nil {
		return fmt.Sprintf("no such field: {typeinfomissing}.%s", e.Field)
	}
	return fmt.Sprintf("no such field: %s.%s", e.Type.Name(), e.Field)
}

// ErrNotUnionStructure means data was fed into a union assembler that can't match the union.
//
// This could have one of several reasons, which are explained in the detail text:
//
//   - there are too many entries in the map;
//   - the keys of critical entries aren't found;
//   - keys are found that aren't any of the expected critical keys;
//   - etc.
//
// TypeName is currently a string... see comments at the top of this file for
// remarks on the issues we need to address about these identifiers in errors in general.
type ErrNotUnionStructure struct {
	TypeName string

	Detail string
}

func (e ErrNotUnionStructure) Error() string {
	return fmt.Sprintf("cannot match schema: union structure constraints for %s caused rejection: %s", e.TypeName, e.Detail)
}
