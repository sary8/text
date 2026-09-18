// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plural

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/language"
)

func TestGetIntApprox(t *testing.T) {
	// big must be a multiple of 10^nMod for all values of nMod used below.
	const big = 1000000000
	testCases := []struct {
		digits string
		start  int
		end    int
		nMod   int
		want   int
	}{
		{"123", 0, 1, 1, 1},
		{"123", 0, 2, 1, big + 2},
		{"123", 0, 2, 2, 12},
		{"123", 3, 4, 2, 0},
		{"12345", 3, 4, 2, 4},
		{"40", 0, 1, 2, 4},
		{"1", 0, 7, 2, big},
		{"1", 0, 100000000, 6, big}, // no loop over the trailing zeros

		{"123", 0, 5, 2, big},
		{"123", 0, 5, 3, big + 300},
		{"123", 0, 5, 4, big + 2300},
		{"123", 0, 5, 5, 12300},
		{"123", 0, 5, 6, 12300},
		{"123", 0, 5, 7, 12300},

		// The value modulo powers of ten up to 10^nMod is preserved for large
		// numbers.
		{"1000001", 0, 7, 6, big + 1},
		{"1234567", 0, 7, 6, big + 234567},
		{"2500000", 0, 7, 6, big + 500000},
		{"12", 0, 9, 6, big}, // 120000000 is a multiple of 10^6

		// Translation of examples in MatchDigits.
		// Integer parts
		{"123", 0, 3, 3, 123},  // 123
		{"1234", 0, 3, 3, 123}, // 123.4
		{"1", 0, 6, 8, 100000}, // 100000

		// Fraction parts
		{"123", 3, 3, 3, 0},   // 123
		{"1234", 3, 4, 3, 4},  // 123.4
		{"1234", 3, 5, 3, 40}, // 123.40
		{"1", 6, 8, 8, 0},     // 100000.00
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s:%d:%d/%d", tc.digits, tc.start, tc.end, tc.nMod), func(t *testing.T) {
			got := getIntApprox(mkDigits(tc.digits), tc.start, tc.end, tc.nMod, big)
			if got != tc.want {
				t.Errorf("got %d; want %d", got, tc.want)
			}
		})
	}
}

func mkDigits(s string) []byte {
	b := []byte(s)
	for i := range b {
		b[i] -= '0'
	}
	return b
}

func TestValidForms(t *testing.T) {
	testCases := []struct {
		tag  language.Tag
		want []Form
	}{
		{language.AmericanEnglish, []Form{Other, One}},
		{language.Portuguese, []Form{Other, One}},
		{language.Latvian, []Form{Other, Zero, One}},
		{language.Arabic, []Form{Other, Zero, One, Two, Few, Many}},
		{language.Russian, []Form{Other, One, Few, Many}},
	}
	for _, tc := range testCases {
		got := validForms(cardinal, tc.tag)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("validForms(%v): got %v; want %v", tc.tag, got, tc.want)
		}
	}
}

// TestMatchPluralV checks that a relation v = x does not match a number with
// 100 or more visible fraction digits, which the sets cannot represent.
func TestMatchPluralV(t *testing.T) {
	en := language.English
	if got := Cardinal.MatchPlural(en, 1, 0, 0, 0, 0); got != One {
		t.Errorf("MatchPlural(en, 1): got %v; want %v", got, One)
	}
	for _, v := range []int{99, 100, 101} {
		if got := Cardinal.MatchPlural(en, 1, v, 0, 0, 0); got != Other {
			t.Errorf("MatchPlural(en, 1 with %d fraction digits): got %v; want %v", v, got, Other)
		}
	}
}

func TestOrdinal(t *testing.T) {
	testPlurals(t, Ordinal, ordinalTests)
}

func TestCardinal(t *testing.T) {
	testPlurals(t, Cardinal, cardinalTests)
}

func testPlurals(t *testing.T, p *Rules, testCases []pluralTest) {
	for _, tc := range testCases {
		for _, loc := range strings.Split(tc.locales, " ") {
			tag := language.MustParse(loc)
			// Test integers
			for _, s := range tc.integer {
				a := strings.Split(s, "~")
				from := parseUint(t, a[0])
				to := from
				if len(a) > 1 {
					to = parseUint(t, a[1])
				}
				for n := from; n <= to; n++ {
					t.Run(fmt.Sprintf("%s/int(%d)", loc, n), func(t *testing.T) {
						if f := p.matchComponents(tag, n, 0, 0); f != Form(tc.form) {
							t.Errorf("matchComponents: got %v; want %v", f, Form(tc.form))
						}
						digits := []byte(fmt.Sprint(n))
						for i := range digits {
							digits[i] -= '0'
						}
						if f := p.MatchDigits(tag, digits, len(digits), 0); f != Form(tc.form) {
							t.Errorf("MatchDigits: got %v; want %v", f, Form(tc.form))
						}
					})
				}
			}
			// Test decimals
			for _, s := range tc.decimal {
				a := strings.Split(s, "~")
				from, scale := parseFixedPoint(t, a[0])
				to := from
				if len(a) > 1 {
					var toScale int
					if to, toScale = parseFixedPoint(t, a[1]); toScale != scale {
						t.Fatalf("%s:%s: non-matching scales %d versus %d", loc, s, scale, toScale)
					}
				}
				m := int64(1)
				for i := 0; i < scale; i++ {
					m *= 10
				}
				for n := from; n <= to; n++ {
					num := fmt.Sprintf("%[1]d.%0[3]*[2]d", n/m, n%m, scale)
					name := fmt.Sprintf("%s:dec(%s)", loc, num)
					t.Run(name, func(t *testing.T) {
						i := int(n / m)
						ff := int(n % m)
						tt := ff
						w := scale
						for tt > 0 && tt%10 == 0 {
							w--
							tt /= 10
						}
						if f := p.MatchPlural(tag, i, scale, w, ff, tt); f != Form(tc.form) {
							t.Errorf("MatchPlural: got %v; want %v", f, Form(tc.form))
						}
						if f := p.matchComponents(tag, i, ff, scale); f != Form(tc.form) {
							t.Errorf("matchComponents: got %v; want %v", f, Form(tc.form))
						}
						exp := strings.IndexByte(num, '.')
						digits := []byte(strings.Replace(num, ".", "", 1))
						for i := range digits {
							digits[i] -= '0'
						}
						if f := p.MatchDigits(tag, digits, exp, scale); f != Form(tc.form) {
							t.Errorf("MatchDigits: got %v; want %v", f, Form(tc.form))
						}
					})
				}
			}
		}
	}
}

func parseUint(t *testing.T, s string) int {
	val, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	return int(val)
}

// parseFixedPoint parses a decimal sample. The value is returned as an int64
// as some samples, such as 1000000.0000, do not fit in 32 bits.
func parseFixedPoint(t *testing.T, s string) (val int64, scale int) {
	p := strings.Index(s, ".")
	s = strings.Replace(s, ".", "", 1)
	v, err := strconv.ParseUint(s, 10, 63)
	if err != nil {
		t.Fatal(err)
	}
	return int64(v), len(s) - p
}

// TestMatchDigits covers the approximation of large numbers and fractions in
// MatchDigits and the hard-wired rules at values that the samples in
// data_test.go do not reach.
func TestMatchDigits(t *testing.T) {
	testCases := []struct {
		rules *Rules
		lang  string
		num   string
		want  Form
	}{
		// The integer part is approximated modulo 1000000.
		{Cardinal, "ru", "1000000", Many},
		{Cardinal, "ru", "1000001", One},
		{Cardinal, "ru", "12345678", Many},
		{Cardinal, "br", "2000000", Many},
		{Cardinal, "br", "2500000", Other},

		// The fraction is approximated modulo 100.
		{Cardinal, "bs", "0.123", Few},
		{Cardinal, "lv", "1.201", One},

		// Hard-wired rules.
		{Ordinal, "az", "1100", Few},
		{Ordinal, "az", "2000100", Few},
		{Ordinal, "az", "1109", Other},
		{Ordinal, "it", "800", Many},
		{Ordinal, "it", "801", Other},
		{Ordinal, "it", "1000800", Other},

		// The operand t is the fraction without trailing zeros.
		{Cardinal, "is", "1.10", One},
		{Cardinal, "is", "1.21", One},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s/%s", tc.lang, tc.num), func(t *testing.T) {
			tag := language.MustParse(tc.lang)
			digits := []byte(strings.Replace(tc.num, ".", "", 1))
			for i := range digits {
				digits[i] -= '0'
			}
			exp := strings.IndexByte(tc.num, '.')
			scale := 0
			if exp < 0 {
				exp = len(digits)
			} else {
				scale = len(digits) - exp
			}
			if got := tc.rules.MatchDigits(tag, digits, exp, scale); got != tc.want {
				t.Errorf("MatchDigits: got %v; want %v", got, tc.want)
			}
		})
	}

	// Trailing zeros may be omitted from digits.
	is := language.MustParse("is")
	if got := Cardinal.MatchDigits(is, []byte{1}, 1, 3); got != One { // 1.000
		t.Errorf("MatchDigits(is, 1.000): got %v; want %v", got, One)
	}
	if got := Cardinal.MatchDigits(is, []byte{1, 1}, 1, 3); got != One { // 1.100
		t.Errorf("MatchDigits(is, 1.100): got %v; want %v", got, One)
	}

	// Leading zeros of the fraction may be omitted as well, so exp may be
	// negative and digits may be empty.
	en := language.English
	for _, tc := range []struct {
		digits     []byte
		exp, scale int
		want       Form
	}{
		{nil, -1, 1, Other},       // 0.0
		{[]byte{0}, -1, 2, Other}, // 0.00
		{[]byte{1}, -1, 2, Other}, // 0.01
	} {
		if got := Cardinal.MatchDigits(en, tc.digits, tc.exp, tc.scale); got != tc.want {
			t.Errorf("MatchDigits(en, %v, %d, %d): got %v; want %v", tc.digits, tc.exp, tc.scale, got, tc.want)
		}
	}
	if got := Cardinal.MatchDigits(is, []byte{1}, -1, 2); got != One { // 0.01: t = 1
		t.Errorf("MatchDigits(is, 0.01): got %v; want %v", got, One)
	}
}

// TestMatchPluralW checks that the value of w does not influence the result,
// as documented.
func TestMatchPluralW(t *testing.T) {
	testCases := []struct {
		lang       string
		i, v, f, t int
	}{
		{"is", 1, 2, 10, 1},
		{"fr", 1000000, 0, 0, 0},
		{"ru", 21, 1, 0, 0},
		{"lv", 1, 3, 201, 201},
	}
	for _, tc := range testCases {
		tag := language.MustParse(tc.lang)
		want := Cardinal.MatchPlural(tag, tc.i, tc.v, 0, tc.f, tc.t)
		if got := Cardinal.MatchPlural(tag, tc.i, tc.v, 5, tc.f, tc.t); got != want {
			t.Errorf("MatchPlural(%s, %d, %d, w, %d, %d): got %v for w=5; want %v as for w=0", tc.lang, tc.i, tc.v, tc.f, tc.t, got, want)
		}
	}
}

func BenchmarkPluralSimpleCases(b *testing.B) {
	p := Cardinal
	en := tagToID(language.English)
	zh := tagToID(language.Chinese)
	for i := 0; i < b.N; i++ {
		matchPlural(p, en, 0, 0, 0, 0)    // 0
		matchPlural(p, en, 1, 0, 0, 0)    // 1
		matchPlural(p, en, 2, 120, 12, 3) // 2.120
		matchPlural(p, zh, 0, 0, 0, 0)    // 0
		matchPlural(p, zh, 1, 0, 0, 0)    // 1
		matchPlural(p, zh, 2, 120, 12, 3) // 2.120
	}
}

func BenchmarkPluralComplexCases(b *testing.B) {
	p := Cardinal
	ar := tagToID(language.Arabic)
	lv := tagToID(language.Latvian)
	for i := 0; i < b.N; i++ {
		matchPlural(p, lv, 0, 19, 19, 2)      // 0.19
		matchPlural(p, lv, 11, 0, 0, 3)       // 11.000
		matchPlural(p, lv, 100, 1230, 123, 4) // 100.1230
		matchPlural(p, ar, 0, 0, 0, 0)        // 0
		matchPlural(p, ar, 110, 0, 0, 0)      // 110
		matchPlural(p, ar, 99, 99, 99, 2)     // 99.99
	}
}
