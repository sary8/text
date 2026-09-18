// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package language

// CompactCoreInfo is a compact integer with the three core tags encoded. The
// language ID occupies the most significant bits, so that sorting
// CompactCoreInfo values groups tags by language.
type CompactCoreInfo uint32

// The number of bits used by each of the core tags in a CompactCoreInfo.
// The remaining 13 bits hold the language ID.
const (
	cciRegionBits = 10
	cciScriptBits = 9
	cciLangShift  = cciScriptBits + cciRegionBits
)

// GetCompactCore generates a uint32 value that is guaranteed to be unique for
// different language, region, and script values. It returns false if t has no
// language index or if one of its components does not fit in the bits
// reserved for it.
func GetCompactCore(t Tag) (cci CompactCoreInfo, ok bool) {
	if t.LangID >= langNoIndexOffset {
		return 0, false
	}
	if t.LangID >= 1<<(32-cciLangShift) ||
		t.ScriptID >= 1<<cciScriptBits ||
		t.RegionID >= 1<<cciRegionBits {
		return 0, false
	}
	cci |= CompactCoreInfo(t.LangID) << cciLangShift
	cci |= CompactCoreInfo(t.ScriptID) << cciRegionBits
	cci |= CompactCoreInfo(t.RegionID)
	return cci, true
}

// Tag generates a tag from c.
func (c CompactCoreInfo) Tag() Tag {
	return Tag{
		LangID:   Language(c >> cciLangShift),
		ScriptID: Script(c>>cciRegionBits) & (1<<cciScriptBits - 1),
		RegionID: Region(c & (1<<cciRegionBits - 1)),
	}
}
