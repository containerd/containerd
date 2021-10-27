// The MIT License (MIT)
// Copyright (c) 2014 Andreas Briese, eduToolbox@Bri-C GmbH, Sarstedt

// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
// FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
// COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
// IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
// CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

package bbloom

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"math"
	"math/bits"
	"sync"
)

func getSize(ui64 uint64) (size uint64, exponent uint64) {
	if ui64 < uint64(512) {
		ui64 = uint64(512)
	}
	size = uint64(1)
	for size < ui64 {
		size <<= 1
		exponent++
	}
	return size, exponent
}

func calcSizeByWrongPositives(numEntries, wrongs float64) (uint64, uint64) {
	size := -1 * numEntries * math.Log(wrongs) / math.Pow(float64(0.69314718056), 2)
	locs := math.Ceil(float64(0.69314718056) * size / numEntries)
	return uint64(size), uint64(locs)
}

var ErrUsage = errors.New("usage: New(float64(number_of_entries), float64(number_of_hashlocations)) i.e. New(float64(1000), float64(3)) or New(float64(number_of_entries), float64(ratio_of_false_positives)) i.e. New(float64(1000), float64(0.03))")
var ErrInvalidParms = errors.New("One of parameters was outside of allowed range")

// New
// returns a new bloomfilter
func New(params ...float64) (bloomfilter *Bloom, err error) {
	var entries, locs uint64
	if len(params) == 2 {
		if params[0] < 0 || params[1] < 0 {
			return nil, ErrInvalidParms
		}
		if params[1] < 1 {
			entries, locs = calcSizeByWrongPositives(math.Max(params[0], 1), params[1])
		} else {
			entries, locs = uint64(params[0]), uint64(params[1])
		}
	} else {
		return nil, ErrUsage
	}
	size, exponent := getSize(uint64(entries))
	bloomfilter = &Bloom{
		sizeExp: exponent,
		size:    size - 1,
		setLocs: locs,
		shift:   64 - exponent,
		bitset:  make([]uint64, size>>6),
	}
	return bloomfilter, nil
}

// NewWithBoolset
// takes a []byte slice and number of locs per entry
// returns the bloomfilter with a bitset populated according to the input []byte
func NewWithBoolset(bs []byte, locs uint64) (bloomfilter *Bloom) {
	bloomfilter, err := New(float64(len(bs)<<3), float64(locs))
	if err != nil {
		panic(err) // Should never happen
	}
	for i := range bloomfilter.bitset {
		bloomfilter.bitset[i] = binary.BigEndian.Uint64((bs)[i<<3:])
	}
	return bloomfilter
}

// bloomJSONImExport
// Im/Export structure used by JSONMarshal / JSONUnmarshal
type bloomJSONImExport struct {
	FilterSet []byte
	SetLocs   uint64
}

//
// Bloom filter
type Bloom struct {
	Mtx     sync.RWMutex
	bitset  []uint64
	sizeExp uint64
	size    uint64
	setLocs uint64
	shift   uint64

	content uint64
}

// ElementsAdded returns the number of elements added to the bloom filter.
func (bl *Bloom) ElementsAdded() uint64 {
	return bl.content
}

// <--- http://www.cse.yorku.ca/~oz/hash.html
// modified Berkeley DB Hash (32bit)
// hash is casted to l, h = 16bit fragments
// func (bl Bloom) absdbm(b *[]byte) (l, h uint64) {
// 	hash := uint64(len(*b))
// 	for _, c := range *b {
// 		hash = uint64(c) + (hash << 6) + (hash << bl.sizeExp) - hash
// 	}
// 	h = hash >> bl.shift
// 	l = hash << bl.shift >> bl.shift
// 	return l, h
// }

// Update: found sipHash of Jean-Philippe Aumasson & Daniel J. Bernstein to be even faster than absdbm()
// https://131002.net/siphash/
// siphash was implemented for Go by Dmitry Chestnykh https://github.com/dchest/siphash

// Add
// set the bit(s) for entry; Adds an entry to the Bloom filter
func (bl *Bloom) Add(entry []byte) {
	bl.content++
	l, h := bl.sipHash(entry)
	for i := uint64(0); i < (*bl).setLocs; i++ {
		bl.set((h + i*l) & (*bl).size)
	}
}

// AddTS
// Thread safe: Mutex.Lock the bloomfilter for the time of processing the entry
func (bl *Bloom) AddTS(entry []byte) {
	bl.Mtx.Lock()
	bl.Add(entry)
	bl.Mtx.Unlock()
}

// Has
// check if bit(s) for entry is/are set
// returns true if the entry was added to the Bloom Filter
func (bl *Bloom) Has(entry []byte) bool {
	l, h := bl.sipHash(entry)
	res := true
	for i := uint64(0); i < bl.setLocs; i++ {
		res = res && bl.isSet((h+i*l)&bl.size)
		// Branching here (early escape) is not worth it
		// This is my conclusion from benchmarks
		// (prevents loop unrolling)
		// if !res {
		//   return false
		// }
	}
	return res
}

// HasTS
// Thread safe: Mutex.Lock the bloomfilter for the time of processing the entry
func (bl *Bloom) HasTS(entry []byte) bool {
	bl.Mtx.RLock()
	has := bl.Has(entry[:])
	bl.Mtx.RUnlock()
	return has
}

// AddIfNotHas
// Only Add entry if it's not present in the bloomfilter
// returns true if entry was added
// returns false if entry was allready registered in the bloomfilter
func (bl *Bloom) AddIfNotHas(entry []byte) (added bool) {
	l, h := bl.sipHash(entry)
	contained := true
	for i := uint64(0); i < bl.setLocs; i++ {
		prev := bl.getSet((h + i*l) & bl.size)
		contained = contained && prev
	}
	if !contained {
		bl.content++
	}
	return !contained
}

// AddIfNotHasTS
// Tread safe: Only Add entry if it's not present in the bloomfilter
// returns true if entry was added
// returns false if entry was allready registered in the bloomfilter
func (bl *Bloom) AddIfNotHasTS(entry []byte) (added bool) {
	bl.Mtx.Lock()
	added = bl.AddIfNotHas(entry[:])
	bl.Mtx.Unlock()
	return added
}

// Clear
// resets the Bloom filter
func (bl *Bloom) Clear() {
	bs := bl.bitset // important performance optimization.
	for i := range bs {
		bs[i] = 0
	}
	bl.content = 0
}

// ClearTS clears the bloom filter (thread safe).
func (bl *Bloom) ClearTS() {
	bl.Mtx.Lock()
	bl.Clear()
	bl.Mtx.Unlock()
}

func (bl *Bloom) set(idx uint64) {
	bl.bitset[idx>>6] |= 1 << (idx % 64)
}

func (bl *Bloom) getSet(idx uint64) bool {
	cur := bl.bitset[idx>>6]
	bit := uint64(1 << (idx % 64))
	bl.bitset[idx>>6] = cur | bit
	return (cur & bit) > 0
}

func (bl *Bloom) isSet(idx uint64) bool {
	return bl.bitset[idx>>6]&(1<<(idx%64)) > 0
}

func (bl *Bloom) marshal() bloomJSONImExport {
	bloomImEx := bloomJSONImExport{}
	bloomImEx.SetLocs = uint64(bl.setLocs)
	bloomImEx.FilterSet = make([]byte, len(bl.bitset)<<3)
	for i, w := range bl.bitset {
		binary.BigEndian.PutUint64(bloomImEx.FilterSet[i<<3:], w)
	}
	return bloomImEx
}

// JSONMarshal
// returns JSON-object (type bloomJSONImExport) as []byte
func (bl *Bloom) JSONMarshal() []byte {
	data, err := json.Marshal(bl.marshal())
	if err != nil {
		log.Fatal("json.Marshal failed: ", err)
	}
	return data
}

// JSONMarshalTS is a thread-safe version of JSONMarshal
func (bl *Bloom) JSONMarshalTS() []byte {
	bl.Mtx.RLock()
	export := bl.marshal()
	bl.Mtx.RUnlock()
	data, err := json.Marshal(export)
	if err != nil {
		log.Fatal("json.Marshal failed: ", err)
	}
	return data
}

// JSONUnmarshal
// takes JSON-Object (type bloomJSONImExport) as []bytes
// returns bloom32 / bloom64 object
func JSONUnmarshal(dbData []byte) (*Bloom, error) {
	bloomImEx := bloomJSONImExport{}
	err := json.Unmarshal(dbData, &bloomImEx)
	if err != nil {
		return nil, err
	}
	bf := NewWithBoolset(bloomImEx.FilterSet, bloomImEx.SetLocs)
	return bf, nil
}

// FillRatio returns the fraction of bits set.
func (bl *Bloom) FillRatio() float64 {
	count := uint64(0)
	for _, b := range bl.bitset {
		count += uint64(bits.OnesCount64(b))
	}
	return float64(count) / float64(bl.size+1)
}

// FillRatioTS is a thread-save version of FillRatio
func (bl *Bloom) FillRatioTS() float64 {
	bl.Mtx.RLock()
	fr := bl.FillRatio()
	bl.Mtx.RUnlock()
	return fr
}

// // alternative hashFn
// func (bl Bloom) fnv64a(b *[]byte) (l, h uint64) {
// 	h64 := fnv.New64a()
// 	h64.Write(*b)
// 	hash := h64.Sum64()
// 	h = hash >> 32
// 	l = hash << 32 >> 32
// 	return l, h
// }
//
// // <-- http://partow.net/programming/hashfunctions/index.html
// // citation: An algorithm proposed by Donald E. Knuth in The Art Of Computer Programming Volume 3,
// // under the topic of sorting and search chapter 6.4.
// // modified to fit with boolset-length
// func (bl Bloom) DEKHash(b *[]byte) (l, h uint64) {
// 	hash := uint64(len(*b))
// 	for _, c := range *b {
// 		hash = ((hash << 5) ^ (hash >> bl.shift)) ^ uint64(c)
// 	}
// 	h = hash >> bl.shift
// 	l = hash << bl.sizeExp >> bl.sizeExp
// 	return l, h
// }
