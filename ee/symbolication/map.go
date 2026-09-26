// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

// Package symbolication builds the index of a source map and resolves
// stack frames through it.
package symbolication

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrInvalidMap reports a file that is not a usable source map. Retrying
// cannot fix it.
var ErrInvalidMap = errors.New("invalid source map")

// Map is a parsed source map.
type Map struct {
	Sources []string
	// SourcesContent is the text of each source, "" when the map carries none.
	SourcesContent []string
	Names          []string
	Ignored        []bool
	Segments       []Segment
	Lines          int
	// FunctionOffsets is Hermes' x_hermes_function_offsets for segment 0: the
	// bytecode offset each function starts at.
	FunctionOffsets []uint32
}

// Segment maps every generated position from (Line, Column) up to the next
// segment to one original position. Source and Name are NoIndex when the
// segment carries none.
type Segment struct {
	Line   uint32
	Column uint32
	Source uint32
	// OriginalLine and OriginalColumn are zero-based, as the map encodes them.
	OriginalLine   uint32
	OriginalColumn uint32
	Name           uint32
}

// NoIndex is the Source or Name of a segment that names none.
const NoIndex = ^uint32(0)

type rawMap struct {
	Version           int                 `json:"version"`
	Sources           []string            `json:"sources"`
	Names             []string            `json:"names"`
	Mappings          string              `json:"mappings"`
	SourcesContent    []*string           `json:"sourcesContent"`
	IgnoreList        []int               `json:"ignoreList"`
	GoogleIgnoreList  []int               `json:"x_google_ignoreList"`
	HermesFuncOffsets map[string][]uint32 `json:"x_hermes_function_offsets"`
}

// Parse decodes a source map. Any shape the index cannot use is ErrInvalidMap.
func Parse(data []byte) (*Map, error) {
	var raw rawMap
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMap, err)
	}
	if raw.Version != 3 {
		return nil, fmt.Errorf("%w: version %d, expected 3", ErrInvalidMap, raw.Version)
	}
	if raw.Mappings == "" {
		return nil, fmt.Errorf("%w: no mappings", ErrInvalidMap)
	}
	segments, lines, err := decodeMappings(raw.Mappings, len(raw.Sources), len(raw.Names))
	if err != nil {
		return nil, err
	}
	ignored := make([]bool, len(raw.Sources))
	for _, list := range [][]int{raw.IgnoreList, raw.GoogleIgnoreList} {
		for _, index := range list {
			if index >= 0 && index < len(ignored) {
				ignored[index] = true
			}
		}
	}
	contents := make([]string, len(raw.Sources))
	for i, content := range raw.SourcesContent {
		if i < len(contents) && content != nil {
			contents[i] = *content
		}
	}
	m := &Map{
		Sources:        raw.Sources,
		SourcesContent: contents,
		Names:          raw.Names,
		Ignored:        ignored,
		Segments:       segments,
		Lines:          lines,
	}
	if offsets, ok := raw.HermesFuncOffsets["0"]; ok {
		if !sort.SliceIsSorted(offsets, func(i, j int) bool { return offsets[i] < offsets[j] }) {
			return nil, fmt.Errorf("%w: x_hermes_function_offsets are not sorted", ErrInvalidMap)
		}
		m.FunctionOffsets = offsets
	}
	return m, nil
}

const base64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var base64Values = func() [256]int8 {
	var values [256]int8
	for i := range values {
		values[i] = -1
	}
	for i := 0; i < len(base64Alphabet); i++ {
		values[base64Alphabet[i]] = int8(i)
	}
	return values
}()

// decodeMappings turns the VLQ string into absolute segments, in generated
// order, which is the order the string lists them in.
func decodeMappings(mappings string, sources, names int) ([]Segment, int, error) {
	segments := make([]Segment, 0, len(mappings)/5)
	var totals runningTotals
	lines := strings.Split(mappings, ";")
	for lineIndex, line := range lines {
		totals.column = 0
		for _, encoded := range strings.Split(line, ",") {
			if encoded == "" {
				continue
			}
			deltas, err := decodeVLQ(encoded)
			if err != nil {
				return nil, 0, err
			}
			segment, err := totals.add(deltas, sources, names)
			if err != nil {
				return nil, 0, err
			}
			segment.Line = uint32(lineIndex)
			segments = append(segments, segment)
		}
	}
	return segments, len(lines), nil
}

// decodeVLQ reads the numbers of one segment, such as "SAAS" into 9, 0, 0, 9.
func decodeVLQ(encoded string) ([]int64, error) {
	var numbers []int64
	var value int64
	shift := uint(0)
	for i := 0; i < len(encoded); i++ {
		digit := base64Values[encoded[i]]
		if digit < 0 {
			return nil, fmt.Errorf("%w: unexpected character %q in mappings", ErrInvalidMap, encoded[i])
		}
		value |= int64(digit&31) << shift
		// A digit of 32 or more says the number goes on in the next one.
		if digit&32 != 0 {
			shift += 5
			if shift > 35 {
				return nil, fmt.Errorf("%w: VLQ value too long", ErrInvalidMap)
			}
			continue
		}
		// The lowest bit is the sign.
		if value&1 != 0 {
			numbers = append(numbers, -(value >> 1))
		} else {
			numbers = append(numbers, value>>1)
		}
		value, shift = 0, 0
	}
	if shift != 0 {
		return nil, fmt.Errorf("%w: truncated VLQ value", ErrInvalidMap)
	}
	return numbers, nil
}

// runningTotals holds the absolute values each segment's deltas add to.
type runningTotals struct {
	column, source, originalLine, originalColumn, name int64
}

// add applies a segment's deltas and returns the segment they describe.
func (t *runningTotals) add(deltas []int64, sources, names int) (Segment, error) {
	if len(deltas) != 1 && len(deltas) != 4 && len(deltas) != 5 {
		return Segment{}, fmt.Errorf("%w: segment with %d fields", ErrInvalidMap, len(deltas))
	}
	t.column += deltas[0]
	if t.column < 0 {
		return Segment{}, fmt.Errorf("%w: negative position", ErrInvalidMap)
	}
	segment := Segment{Column: uint32(t.column), Source: NoIndex, Name: NoIndex}
	if len(deltas) >= 4 {
		t.source += deltas[1]
		t.originalLine += deltas[2]
		t.originalColumn += deltas[3]
		if t.source < 0 || t.source >= int64(sources) {
			return Segment{}, fmt.Errorf("%w: source index %d out of range", ErrInvalidMap, t.source)
		}
		if t.originalLine < 0 || t.originalColumn < 0 {
			return Segment{}, fmt.Errorf("%w: negative position", ErrInvalidMap)
		}
		segment.Source = uint32(t.source)
		segment.OriginalLine = uint32(t.originalLine)
		segment.OriginalColumn = uint32(t.originalColumn)
	}
	if len(deltas) == 5 {
		t.name += deltas[4]
		if t.name < 0 || t.name >= int64(names) {
			return Segment{}, fmt.Errorf("%w: name index %d out of range", ErrInvalidMap, t.name)
		}
		segment.Name = uint32(t.name)
	}
	return segment, nil
}
