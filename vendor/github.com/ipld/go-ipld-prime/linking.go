package ipld

import (
	"context"
	"io"
)

// This file contains all the functions on LinkSystem.
// These are the helpful, user-facing functions we expect folks to use "most of the time" when loading and storing data.

// Varations:
// - Load vs Store vs ComputeLink
// - With or without LinkContext?
//   - Brevity would be nice but I can't think of what to name the functions, so: everything takes LinkContext.  Zero value is fine though.
// - [for load direction only]: Prototype (and return Node|error) or Assembler (and just return error)?
//   - naming: Load vs Fill.
// - 'Must' variants.

// Can we get as far as a `QuickLoad(lnk Link) (Node, error)` function, which doesn't even ask you for a NodePrototype?
//  No, not quite.  (Alas.)  If we tried to do so, and make it use `basicnode.Prototype`, we'd have import cycles; ded.

func (lsys *LinkSystem) Load(lnkCtx LinkContext, lnk Link, np NodePrototype) (Node, error) {
	nb := np.NewBuilder()
	if err := lsys.Fill(lnkCtx, lnk, nb); err != nil {
		return nil, err
	}
	nd := nb.Build()
	if lsys.NodeReifier == nil {
		return nd, nil
	}
	return lsys.NodeReifier(lnkCtx, nd, lsys)
}

func (lsys *LinkSystem) MustLoad(lnkCtx LinkContext, lnk Link, np NodePrototype) Node {
	if n, err := lsys.Load(lnkCtx, lnk, np); err != nil {
		panic(err)
	} else {
		return n
	}
}

func (lsys *LinkSystem) Fill(lnkCtx LinkContext, lnk Link, na NodeAssembler) error {
	if lnkCtx.Ctx == nil {
		lnkCtx.Ctx = context.Background()
	}
	// Choose all the parts.
	decoder, err := lsys.DecoderChooser(lnk)
	if err != nil {
		return ErrLinkingSetup{"could not choose a decoder", err}
	}
	hasher, err := lsys.HasherChooser(lnk.Prototype())
	if err != nil {
		return ErrLinkingSetup{"could not choose a hasher", err}
	}
	if lsys.StorageReadOpener == nil {
		return ErrLinkingSetup{"no storage configured for reading", io.ErrClosedPipe} // REVIEW: better cause?
	}
	// Open storage, read it, verify it, and feed the codec to assemble the nodes.
	reader, err := lsys.StorageReadOpener(lnkCtx, lnk)
	if err != nil {
		return err
	}
	// TrustaedStorage indicates the data coming out of this reader has already been hashed and verified earlier.
	// As a result, we can skip rehashing it
	if lsys.TrustedStorage {
		return decoder(na, reader)
	}
	// Tee the stream so that the hasher is fed as the unmarshal progresses through the stream.
	tee := io.TeeReader(reader, hasher)
	decodeErr := decoder(na, tee)
	if decodeErr != nil { // It is important to security to check the hash before returning any other observation about the content.
		// This copy is for data remaining the block that wasn't already pulled through the TeeReader by the decoder.
		_, err := io.Copy(hasher, reader)
		if err != nil {
			return err
		}
	}
	hash := hasher.Sum(nil)
	// Bit of a jig to get something we can do the hash equality check on.
	lnk2 := lnk.Prototype().BuildLink(hash)
	if lnk2 != lnk {
		return ErrHashMismatch{Actual: lnk2, Expected: lnk}
	}
	if decodeErr != nil {
		return decodeErr
	}
	return nil
}

func (lsys *LinkSystem) MustFill(lnkCtx LinkContext, lnk Link, na NodeAssembler) {
	if err := lsys.Fill(lnkCtx, lnk, na); err != nil {
		panic(err)
	}
}

func (lsys *LinkSystem) Store(lnkCtx LinkContext, lp LinkPrototype, n Node) (Link, error) {
	if lnkCtx.Ctx == nil {
		lnkCtx.Ctx = context.Background()
	}
	// Choose all the parts.
	encoder, err := lsys.EncoderChooser(lp)
	if err != nil {
		return nil, ErrLinkingSetup{"could not choose an encoder", err}
	}
	hasher, err := lsys.HasherChooser(lp)
	if err != nil {
		return nil, ErrLinkingSetup{"could not choose a hasher", err}
	}
	if lsys.StorageWriteOpener == nil {
		return nil, ErrLinkingSetup{"no storage configured for writing", io.ErrClosedPipe} // REVIEW: better cause?
	}
	// Open storage write stream, feed serial data to the storage and the hasher, and funnel the codec output into both.
	writer, commitFn, err := lsys.StorageWriteOpener(lnkCtx)
	if err != nil {
		return nil, err
	}
	tee := io.MultiWriter(writer, hasher)
	err = encoder(n, tee)
	if err != nil {
		return nil, err
	}
	lnk := lp.BuildLink(hasher.Sum(nil))
	return lnk, commitFn(lnk)
}

func (lsys *LinkSystem) MustStore(lnkCtx LinkContext, lp LinkPrototype, n Node) Link {
	if lnk, err := lsys.Store(lnkCtx, lp, n); err != nil {
		panic(err)
	} else {
		return lnk
	}
}

// ComputeLink returns a Link for the given data, but doesn't do anything else
// (e.g. it doesn't try to store any of the serial-form data anywhere else).
func (lsys *LinkSystem) ComputeLink(lp LinkPrototype, n Node) (Link, error) {
	encoder, err := lsys.EncoderChooser(lp)
	if err != nil {
		return nil, ErrLinkingSetup{"could not choose an encoder", err}
	}
	hasher, err := lsys.HasherChooser(lp)
	if err != nil {
		return nil, ErrLinkingSetup{"could not choose a hasher", err}
	}
	err = encoder(n, hasher)
	if err != nil {
		return nil, err
	}
	return lp.BuildLink(hasher.Sum(nil)), nil
}

func (lsys *LinkSystem) MustComputeLink(lp LinkPrototype, n Node) Link {
	if lnk, err := lsys.ComputeLink(lp, n); err != nil {
		panic(err)
	} else {
		return lnk
	}
}
