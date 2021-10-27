package traversal

import (
	"github.com/ipld/go-ipld-prime"
)

// SelectLinks walks a Node tree and returns a slice of all Links encountered.
// SelectLinks will recurse down into any maps and lists,
// but does not attempt to load any of the links it encounters nor recurse further through them
// (in other words, it's confined to one "block").
//
// SelectLinks only returns the list of links; it does not return any other information
// about them such as position in the tree, etc.
//
// An error may be returned if any of the nodes returns errors during iteration;
// this is generally only possible if one of the Nodes is an ADL,
// and unable to be fully walked because of the inability to load or process some data inside the ADL.
// Nodes already fully in memory should not encounter such errors,
// and it should be safe to ignore errors from this method when used in that situation.
// In case of an error, a partial list will still be returned.
//
// If an identical link is found several times during the walk,
// it is reported several times in the resulting list;
// no deduplication is performed by this method.
func SelectLinks(n ipld.Node) ([]ipld.Link, error) {
	var answer []ipld.Link
	err := accumulateLinks(&answer, n)
	return answer, err
}

func accumulateLinks(a *[]ipld.Link, n ipld.Node) error {
	switch n.Kind() {
	case ipld.Kind_Map:
		for itr := n.MapIterator(); !itr.Done(); {
			_, v, err := itr.Next()
			if err != nil {
				return err
			}
			accumulateLinks(a, v)
		}
	case ipld.Kind_List:
		for itr := n.ListIterator(); !itr.Done(); {
			_, v, err := itr.Next()
			if err != nil {
				return err
			}
			accumulateLinks(a, v)
		}
	case ipld.Kind_Link:
		lnk, _ := n.AsLink()
		*a = append(*a, lnk)
	}
	return nil
}
