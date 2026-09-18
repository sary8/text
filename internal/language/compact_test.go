// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package language

import "testing"

func TestGetCompactCore(t *testing.T) {
	for _, s := range []string{"und", "en", "en-US", "zh-Hant-TW", "vai-Vaii-LR", "sr-Cyrl-RS", "en-Latn-001"} {
		tag, err := Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		cci, ok := GetCompactCore(tag)
		if !ok {
			t.Errorf("GetCompactCore(%s): got !ok; want ok", s)
			continue
		}
		got := cci.Tag()
		if got.LangID != tag.LangID || got.ScriptID != tag.ScriptID || got.RegionID != tag.RegionID {
			t.Errorf("GetCompactCore(%s).Tag(): got %v; want %v", s, got, tag)
		}
	}

	// Components that do not fit in their field are rejected instead of
	// spilling over into the next field.
	for _, tag := range []Tag{
		{ScriptID: 1 << cciScriptBits},
		{RegionID: 1 << cciRegionBits},
		{LangID: langNoIndexOffset},
	} {
		if cci, ok := GetCompactCore(tag); ok {
			t.Errorf("GetCompactCore(%v): got %#x, ok; want !ok", tag, cci)
		}
	}
}
