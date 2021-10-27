package multicodec

import (
	"github.com/ipld/go-ipld-prime"
)

// DefaultRegistry is a multicodec.Registry instance which is global to the program,
// and is used as a default set of codecs.
//
// Some systems (for example, cidlink.DefaultLinkSystem) will use this default registry,
// which makes it easier to write programs that pass fewer explicit arguments around.
// However, these are *only* for default behaviors;
// variations of functions which allow explicit non-default options should always be available
// (for example, cidlink also has other LinkSystem constructor functions which accept an explicit multicodec.Registry,
// and the LookupEncoder and LookupDecoder functions in any LinkSystem can be replaced).
//
// Since this registry is global, mind that there are also some necessary tradeoffs and limitations:
// It can be difficult to control exactly what's present in this global registry
// (Libraries may register codecs in this registry as a side-effect of importing, so even transitive dependencies can affect its content!).
// Also, this registry is only considered safe to modify at package init time.
// If these are concerns for your program, you can create your own multicodec.Registry values,
// and eschew using the global default.
var DefaultRegistry = Registry{}

// RegisterEncoder updates the global DefaultRegistry to map a multicodec indicator number to the given ipld.Encoder function.
// The encoder functions registered can be subsequently looked up using LookupEncoder.
// It is a shortcut to the RegisterEncoder method on the global DefaultRegistry.
//
// Packages which implement an IPLD codec and have a multicodec number associated with them
// are encouraged to register themselves at package init time using this function.
// (Doing this at package init time ensures the default global registry is populated
// without causing race conditions for application code.)
//
// No effort is made to detect conflicting registrations in this map.
// If your dependency tree is such that this becomes a problem,
// there are two ways to address this:
// If RegisterEncoder is called with the same indicator code more than once, the last call wins.
// In practice, this means that if an application has a strong opinion about what implementation for a certain codec,
// then this can be done by making a Register call with that effect at init time in the application's main package.
// This should have the desired effect because the root of the import tree has its init time effect last.
// Alternatively, one can just avoid use of this registry entirely:
// do this by making a LinkSystem that uses a custom EncoderChooser function.
func RegisterEncoder(indicator uint64, encodeFunc ipld.Encoder) {
	DefaultRegistry.RegisterEncoder(indicator, encodeFunc)
}

// LookupEncoder yields an ipld.Encoder function matching a multicodec indicator code number.
// It is a shortcut to the LookupEncoder method on the global DefaultRegistry.
//
// To be available from this lookup function, an encoder must have been registered
// for this indicator number by an earlier call to the RegisterEncoder function.
func LookupEncoder(indicator uint64) (ipld.Encoder, error) {
	return DefaultRegistry.LookupEncoder(indicator)
}

// ListEncoders returns a list of multicodec indicators for which an ipld.Encoder is registered.
// The list is in no particular order.
// It is a shortcut to the ListEncoders method on the global DefaultRegistry.
//
// Be judicious about trying to use this function outside of debugging.
// Because the global default registry is global and easily modified,
// and can be changed by any of the transitive dependencies of your program,
// its contents are not particularly stable.
// In particular, it is not recommended to make any behaviors of your program conditional
// based on information returned by this function -- if your program needs conditional
// behavior based on registred codecs, you may want to consider taking more explicit control
// and using your own non-default registry.
func ListEncoders() []uint64 {
	return DefaultRegistry.ListEncoders()
}

// RegisterDecoder updates the global DefaultRegistry a map a multicodec indicator number to the given ipld.Decoder function.
// The decoder functions registered can be subsequently looked up using LookupDecoder.
// It is a shortcut to the RegisterDecoder method on the global DefaultRegistry.
//
// Packages which implement an IPLD codec and have a multicodec number associated with them
// are encouraged to register themselves in this map at package init time.
// (Doing this at package init time ensures the default global registry is populated
// without causing race conditions for application code.)
//
// No effort is made to detect conflicting registrations in this map.
// If your dependency tree is such that this becomes a problem,
// there are two ways to address this:
// If RegisterDecoder is called with the same indicator code more than once, the last call wins.
// In practice, this means that if an application has a strong opinion about what implementation for a certain codec,
// then this can be done by making a Register call with that effect at init time in the application's main package.
// This should have the desired effect because the root of the import tree has its init time effect last.
// Alternatively, one can just avoid use of this registry entirely:
// do this by making a LinkSystem that uses a custom DecoderChooser function.
func RegisterDecoder(indicator uint64, decodeFunc ipld.Decoder) {
	DefaultRegistry.RegisterDecoder(indicator, decodeFunc)
}

// LookupDecoder yields an ipld.Decoder function matching a multicodec indicator code number.
// It is a shortcut to the LookupDecoder method on the global DefaultRegistry.
//
// To be available from this lookup function, an decoder must have been registered
// for this indicator number by an earlier call to the RegisterDecoder function.
func LookupDecoder(indicator uint64) (ipld.Decoder, error) {
	return DefaultRegistry.LookupDecoder(indicator)
}

// ListDecoders returns a list of multicodec indicators for which an ipld.Decoder is registered.
// The list is in no particular order.
// It is a shortcut to the ListDecoders method on the global DefaultRegistry.
//
// Be judicious about trying to use this function outside of debugging.
// Because the global default registry is global and easily modified,
// and can be changed by any of the transitive dependencies of your program,
// its contents are not particularly stable.
// In particular, it is not recommended to make any behaviors of your program conditional
// based on information returned by this function -- if your program needs conditional
// behavior based on registred codecs, you may want to consider taking more explicit control
// and using your own non-default registry.
func ListDecoders() []uint64 {
	return DefaultRegistry.ListDecoders()
}
