// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

// This file generates data for the CLDR plural rules, as defined in
//    https://unicode.org/reports/tr35/tr35-numbers.html#Language_Plural_Rules
//
// We assume a slightly simplified grammar:
//
// 		condition     = and_condition ('or' and_condition)* samples
// 		and_condition = relation ('and' relation)*
// 		relation      = expr ('=' | '!=') range_list
// 		expr          = operand ('%' '10' '0'* )?
// 		operand       = 'n' | 'i' | 'f' | 't' | 'v' | 'w' | 'e' | 'c'
// 		range_list    = (range | value) (',' range_list)*
// 		range         = value'..'value
// 		value         = digit+
// 		digit         = 0|1|2|3|4|5|6|7|8|9
//
// 		samples       = ('@integer' sampleList)?
// 		                ('@decimal' sampleList)?
// 		sampleList    = sampleRange (',' sampleRange)* (',' ('…'|'...'))?
// 		sampleRange   = decimalValue ('~' decimalValue)?
// 		decimalValue  = value ('.' value)? (('c'|'e') digit+)?
//
//		Symbol	Value
//		n	absolute value of the source number (integer and decimals).
//		i	integer digits of n.
//		v	number of visible fraction digits in n, with trailing zeros.
//		w	number of visible fraction digits in n, without trailing zeros.
//		f	visible fractional digits in n, with trailing zeros.
//		t	visible fractional digits in n, without trailing zeros.
//		e	compact decimal exponent value (c is a synonym).
//
// The algorithm for which the data is generated is based on the following
// observations
//
//    - the number of different sets of numbers which the plural rules use to
//      test inclusion is limited,
//    - most numbers that are tested on are < 100
//
// This allows us to define a bitmap for each number < 100 where a bit i
// indicates whether this number is included in some defined set i.
// The function matchPlural in plural.go defines how we can subsequently use
// this data to determine inclusion.
//
// There are a few rules for which this doesn't work: rules that compare
// against numbers of 100 or more, such as the ordinal rules for Italian and
// Azerbaijani, and rules with a modulus larger than 100, such as the rules
// for Breton and Cornish and the rules for the form "many" of several Romance
// languages. The model here could be extended to handle some of these fairly
// easily (by considering the numbers 100, 200, 300, ..., 800, 900 in addition
// to the first 100), but for now it seems easier to just hard-code these cases.
//
// This package does not support compact decimal formatting, so the operand e
// is always 0. Relations on e are resolved at generation time.

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/internal/gen"
	"golang.org/x/text/internal/language"
	"golang.org/x/text/internal/language/compact"
	"golang.org/x/text/unicode/cldr"
)

var (
	test = flag.Bool("test", false,
		"test existing tables; can be used to compare web data with package data.")
	outputFile     = flag.String("output", "tables.go", "output file")
	outputTestFile = flag.String("testoutput", "data_test.go", "output file")

	draft = flag.String("draft",
		"contributed",
		`Minimal draft requirements (approved, contributed, provisional, unconfirmed).`)
)

func main() {
	gen.Init()

	const pkg = "plural"

	gen.Repackage("gen_common.go", "common.go", pkg)
	// Read the CLDR zip file.
	r := gen.OpenCLDRCoreZip()
	defer r.Close()

	d := &cldr.Decoder{}
	d.SetDirFilter("supplemental", "main")
	d.SetSectionFilter("numbers", "plurals")
	data, err := d.DecodeZip(r)
	if err != nil {
		log.Fatalf("DecodeZip: %v", err)
	}

	// Write the tables of the plural types in a fixed order. The order in
	// which the types appear in the data is that of the files in the CLDR
	// archive, which differs between releases.
	plurals := data.Supplemental().Plurals
	sort.SliceStable(plurals, func(i, j int) bool { return plurals[i].Type < plurals[j].Type })

	w := gen.NewCodeWriter()
	defer w.WriteGoFile(*outputFile, pkg)

	gen.WriteCLDRVersion(w)

	genPlurals(w, data)

	w = gen.NewCodeWriter()
	defer w.WriteGoFile(*outputTestFile, pkg)

	genPluralsTests(w, data)
}

type pluralTest struct {
	locales string   // space-separated list of locales for this test
	form    int      // Use int instead of Form to simplify generation.
	integer []string // Entries of the form \d+ or \d+~\d+
	decimal []string // Entries of the form \f+ or \f+ +~\f+, where f is \d+\.\d+
}

func genPluralsTests(w *gen.CodeWriter, data *cldr.CLDR) {
	w.WriteType(pluralTest{})

	for _, plurals := range data.Supplemental().Plurals {
		if plurals.Type == "" {
			// The empty type is reserved for plural ranges.
			continue
		}
		tests := []pluralTest{}

		for _, pRules := range plurals.PluralRules {
			for _, rule := range pRules.PluralRule {
				test := pluralTest{
					locales: pRules.Locales,
					form:    int(countMap[rule.Count]),
				}
				scan := bufio.NewScanner(strings.NewReader(rule.Data()))
				scan.Split(splitTokens)
				var p *[]string
				for scan.Scan() {
					switch t := scan.Text(); t {
					case "@integer":
						p = &test.integer
					case "@decimal":
						p = &test.decimal
					case ",", "…":
					default:
						// Samples such as 1c6 and 1.1c6 are numbers in compact
						// decimal notation, for which the form may depend on
						// the exponent. This package does not support compact
						// decimal formatting, so these samples are skipped.
						if p != nil && !strings.ContainsAny(t, "ce") {
							*p = append(*p, t)
						}
					}
				}
				tests = append(tests, test)
			}
		}
		w.WriteVar(plurals.Type+"Tests", tests)
	}
}

func genPlurals(w *gen.CodeWriter, data *cldr.CLDR) {
	for _, plurals := range data.Supplemental().Plurals {
		if plurals.Type == "" {
			continue
		}
		// Initialize setMap and inclusionMasks. They are already populated with
		// a few entries to serve as an example and to assign nice numbers to
		// common cases.

		// setMap contains sets of numbers represented by boolean arrays where
		// a true value for element i means that the number i is included.
		setMap := map[[numN]bool]int{
			// The above init func adds an entry for including all numbers.
			[numN]bool{1: true}: 1, // fix {1} to a nice value
			[numN]bool{2: true}: 2, // fix {2} to a nice value
			[numN]bool{0: true}: 3, // fix {0} to a nice value
		}

		// inclusionMasks contains bit masks for every number under numN to
		// indicate in which set the number is included. Bit 1 << x will be set
		// if it is included in set x.
		inclusionMasks := [numN]uint64{
			// Note: these entries are not complete: more bits will be set along the way.
			0: 1 << 3,
			1: 1 << 1,
			2: 1 << 2,
		}

		// Create set {0..99}. We will assign this set the identifier 0.
		var all [numN]bool
		for i := range all {
			// Mark number i as being included in the set (which has identifier 0).
			inclusionMasks[i] |= 1 << 0
			// Mark number i as included in the set.
			all[i] = true
		}
		// Register the identifier for the set.
		setMap[all] = 0

		rules := []pluralCheck{}
		index := []byte{0}
		langMap := map[compact.ID]byte{0: 0}

		for _, pRules := range plurals.PluralRules {
			// Parse the rules.
			var conds []orCondition
			for _, rule := range pRules.PluralRule {
				form := countMap[rule.Count]
				conds = parsePluralCondition(conds, rule.Data(), form)
			}
			// Encode the rules.
			for _, c := range conds {
				// If an or condition only has filters, we create an entry for
				// this filter and the set that contains all values.
				empty := len(c.specials) == 0
				for _, b := range c.used {
					empty = empty && !b
				}
				if empty {
					rules = append(rules, pluralCheck{
						cat:   byte(opMod<<opShift) | byte(c.form),
						setID: 0, // all values
					})
					continue
				}
				// We have some entries with values.
				for i, set := range c.set {
					if !c.used[i] {
						continue
					}
					index, ok := setMap[set]
					if !ok {
						index = len(setMap)
						setMap[set] = index
						for i := range inclusionMasks {
							if set[i] {
								inclusionMasks[i] |= 1 << uint64(index)
							}
						}
					}
					rules = append(rules, pluralCheck{
						cat:   byte(i<<opShift | andNext),
						setID: byte(index),
					})
				}
				for _, s := range c.specials {
					rules = append(rules, pluralCheck{
						cat:   byte(opSpecial<<opShift | andNext),
						setID: byte(s),
					})
				}
				// Now set the last entry to the plural form the rule matches.
				rules[len(rules)-1].cat &^= formMask
				rules[len(rules)-1].cat |= byte(c.form)
			}
			// Point the relevant locales to the created entries.
			for _, loc := range strings.Split(pRules.Locales, " ") {
				if strings.TrimSpace(loc) == "" {
					continue
				}
				lang, ok := compact.FromTag(language.MustParse(loc))
				if !ok {
					// FromTag returns the index of an ancestor if the tag has none.
					log.Fatalf("No compact index for locale %q", loc)
				}
				langMap[lang] = byte(len(index) - 1)
			}
			index = append(index, byte(len(rules)))
		}
		w.WriteVar(plurals.Type+"Rules", rules)
		w.WriteVar(plurals.Type+"Index", index)
		// Expand the values: first by using the parent relationship.
		langToIndex := make([]byte, compact.NumCompactTags)
		for i := range langToIndex {
			for p := compact.ID(i); ; p = p.Parent() {
				if x, ok := langMap[p]; ok {
					langToIndex[i] = x
					break
				}
			}
		}
		// Now expand by including entries with identical languages for which
		// one isn't set.
		for i, v := range langToIndex {
			if v == 0 {
				id, _ := compact.FromTag(language.Tag{
					LangID: compact.ID(i).Tag().LangID,
				})
				if p := langToIndex[id]; p != 0 {
					langToIndex[i] = p
				}
			}
		}
		w.WriteVar(plurals.Type+"LangToIndex", langToIndex)
		// Need to convert array to slice because of golang.org/issue/7651.
		// This will allow tables to be dropped when unused. This is especially
		// relevant for the ordinal data, which I suspect won't be used as much.
		w.WriteVar(plurals.Type+"InclusionMasks", inclusionMasks[:])

		if len(rules) > 0xFF {
			log.Fatalf("Too many entries for rules: %#x", len(rules))
		}
		if len(index) > 0xFF {
			log.Fatalf("Too many entries for index: %#x", len(index))
		}
		if len(setMap) > 64 { // maximum number of bits.
			log.Fatalf("Too many entries for setMap: %d", len(setMap))
		}
		w.WriteComment(
			"Slots used for %s: %X of 0xFF rules; %X of 0xFF indexes; %d of 64 sets",
			plurals.Type, len(rules), len(index), len(setMap))
		// Prevent comment from attaching to the next entry.
		fmt.Fprint(w, "\n\n")
	}
}

// orCondition holds the constraints of a single and_condition of a plural
// rule, which is one of the alternatives of the condition of the rule.
type orCondition struct {
	original string // for debugging

	form Form
	used [32]bool
	set  [32][numN]bool

	// specials holds the hard-wired rules that must match in addition to
	// the sets.
	specials []specialRule
}

func newOrCondition(original string, f Form) orCondition {
	cond := orCondition{original: original, form: f}
	// Set all numbers to be allowed for all number classes and restrict
	// from here on.
	for i := range cond.set {
		for j := range cond.set[i] {
			cond.set[i][j] = true
		}
	}
	return cond
}

// add restricts the set for op to the numbers whose value modulo mod (or the
// number itself if mod is 0) is in v. All values in v must be less than
// maxMod.
func (o *orCondition) add(op opID, mod int, v []int) {
	for _, x := range v {
		if x >= maxMod {
			log.Fatalf("Value %d not supported: %s", x, o.original)
		}
	}
	for i := 0; i < numN; i++ {
		m := i
		if mod != 0 {
			m = i % mod
		}
		if !slices.Contains(v, m) {
			o.set[op][i] = false
		}
	}
	o.used[op] = true
}

func intRange(from, to int) []int {
	var a []int
	for ; from <= to; from++ {
		a = append(a, from)
	}
	return a
}

var operandIndex = map[string]opID{
	"i": opI,
	"n": opN,
	"f": opF,
	"v": opV,
	"t": opT,
}

// relation is a parsed relation of a plural rule: an operand, optionally
// taken modulo mod, compared against a list of values.
type relation struct {
	operand  string // n, i, f, t, v, w, e or c
	mod      int    // 0 if no modulus is applied
	notEqual bool
	values   []int
}

// parsePluralCondition parses the condition of a single pluralRule and appends
// the resulting or conditions to conds.
//
// Example rules:
//
//	// Category "one" in English: only allow 1 with no visible fraction
//	i = 1 and v = 0 @integer 1
//
//	// Category "few" in Czech: all numbers with visible fractions
//	v != 0   @decimal ...
//
//	// Category "zero" in Latvian: all multiples of 10 or the numbers 11-19 or
//	// numbers with a fraction 11..19 and no trailing zeros.
//	n % 10 = 0 or n % 100 = 11..19 or v = 2 and f % 100 = 11..19 @integer ...
//
// @integer and @decimal are followed by examples and are not relevant for the
// rule itself. The are used here to signal the termination of the rule.
func parsePluralCondition(conds []orCondition, s string, f Form) []orCondition {
	scan := bufio.NewScanner(strings.NewReader(s))
	scan.Split(splitTokens)
	for {
		// The or conditions for the current and_condition. There is usually
		// only one, but a relation may need to be split into two alternatives
		// and a relation on e may make the and_condition impossible.
		active := []orCondition{newOrCondition(s, f)}
	andLoop:
		for {
			scan.Scan() // Must exist.
			switch operand := scan.Text(); operand {
			case "n", "i", "f", "t", "v", "w", "e", "c":
				rel, token := parseRelation(scan, operand)
				active = addRelation(active, rel, s)
				switch token {
				case "or":
					conds = append(conds, active...)
					break andLoop
				case "@integer", "@decimal": // examples
					// There is always an example in practice, so we always
					// terminate here.
					if err := scan.Err(); err != nil {
						log.Fatal(err)
					}
					return append(conds, active...)
				case "and":
					// keep accumulating
				default:
					log.Fatalf("Unexpected token %q", token)
				}
			case "@integer", "@decimal": // "other" entry: tests only.
				return conds
			default:
				log.Fatalf("Unexpected operand %q (%s)", operand, s)
			}
		}
	}
}

// parseRelation parses a relation for the given operand and returns it together
// with the token following the relation.
func parseRelation(scan *bufio.Scanner, operand string) (rel relation, next string) {
	rel.operand = operand
	op := scanToken(scan)
	if op == "%" {
		rel.mod = scanUint(scan)
		op = scanToken(scan)
	}
	switch op {
	case "=":
	case "!=":
		rel.notEqual = true
	default:
		log.Fatalf("Unexpected op %q", op)
	}
	for {
		v := scanUint(scan)
		next = scanToken(scan)
		if next == ".." {
			rel.values = append(rel.values, intRange(v, scanUint(scan))...)
			next = scanToken(scan)
		} else {
			rel.values = append(rel.values, v)
		}
		if next != "," {
			return rel, next
		}
	}
}

// addRelation adds the constraints of rel to each of the active or conditions
// and returns the resulting or conditions.
func addRelation(active []orCondition, rel relation, rule string) []orCondition {
	switch rel.operand {
	case "e", "c":
		// This package does not support compact decimal formatting, so e is
		// always 0 and the relation either always or never holds.
		if slices.Contains(rel.values, 0) != rel.notEqual {
			return active
		}
		return nil
	case "w":
		// The number of visible fraction digits without trailing zeros is only
		// ever compared to zero, in which case it is equivalent to comparing t
		// to zero.
		if len(rel.values) != 1 || rel.values[0] != 0 {
			log.Fatalf("Must compare against zero for operand w: %s", rule)
		}
		rel.operand = "t"
	}
	if rel.mod == 10 || rel.mod == 100 || rel.mod == 0 && slices.Max(rel.values) < maxMod {
		// The relation can be expressed with the inclusion masks.
		op := operandIndex[rel.operand]
		if rel.mod != 0 {
			op |= opMod
		}
		if rel.notEqual {
			op |= opNotEqual
		}
		for i := range active {
			active[i].add(op, rel.mod, rel.values)
		}
		return active
	}
	if rel.notEqual || rel.operand != "n" && rel.operand != "i" {
		log.Fatalf("Relation not supported: %s", rule)
	}
	// Rules with relations on numbers of 100 or more or with a larger modulus
	// are hard-wired.
	var specials []specialRule
	var small []int
	switch {
	case rel.mod == 0:
		// Split the values in the ones that can be handled by the inclusion
		// masks and the ones that need a hard-wired rule. As the number can
		// only be in one of the two sets, these become separate alternatives.
		var large []int
		for _, v := range rel.values {
			if v < maxMod {
				small = append(small, v)
			} else {
				large = append(large, v)
			}
		}
		switch {
		case slices.Equal(large, []int{800}):
			specials = append(specials, specialIs800)
		case slices.Equal(large, intRange(800, 899)):
			specials = append(specials, specialIs800To899)
		default:
			log.Fatalf("Values not supported: %s", rule)
		}
	case rel.mod == 1000 && slices.Equal(rel.values, []int{0}):
		specials = append(specials, specialMod1e3Zero)
	case rel.mod == 1000 && slices.Equal(rel.values, []int{100, 200, 300, 400, 500, 600, 700, 800, 900}):
		specials = append(specials, specialMod1e3Hundreds)
	case rel.mod == 100000 && slices.Equal(rel.values, append(intRange(1000, 20000), 40000, 60000, 80000)):
		specials = append(specials, specialMod1e5Thousands)
	case rel.mod == 1000000 && slices.Equal(rel.values, []int{0}):
		specials = append(specials, specialMod1e6Zero)
	case rel.mod == 1000000 && slices.Equal(rel.values, []int{100000}):
		specials = append(specials, specialMod1e6Is1e5)
	default:
		log.Fatalf("Modulo value not supported: %s", rule)
	}
	var result []orCondition
	for _, cond := range active {
		if len(small) > 0 {
			c := cond
			c.add(operandIndex[rel.operand], 0, small)
			result = append(result, c)
		}
		// The hard-wired rules only consider the integer part of the number.
		// For operand n, the number must not have a fraction.
		if rel.operand == "n" {
			cond.add(opF, 0, []int{0})
		}
		// Copy on append: the or conditions may share the backing array.
		cond.specials = append(cond.specials[:len(cond.specials):len(cond.specials)], specials...)
		result = append(result, cond)
	}
	return result
}

func scanToken(scan *bufio.Scanner) string {
	scan.Scan()
	return scan.Text()
}

func scanUint(scan *bufio.Scanner) int {
	scan.Scan()
	val, err := strconv.ParseUint(scan.Text(), 10, 32)
	if err != nil {
		log.Fatal(err)
	}
	return int(val)
}

// splitTokens can be used with bufio.Scanner to tokenize CLDR plural rules.
func splitTokens(data []byte, atEOF bool) (advance int, token []byte, err error) {
	condTokens := [][]byte{
		[]byte(".."),
		[]byte(","),
		[]byte("!="),
		[]byte("="),
	}
	advance, token, err = bufio.ScanWords(data, atEOF)
	for _, t := range condTokens {
		if len(t) >= len(token) {
			continue
		}
		switch p := bytes.Index(token, t); {
		case p == -1:
		case p == 0:
			advance = len(t)
			token = token[:len(t)]
			return advance - len(token) + len(t), token[:len(t)], err
		case p < advance:
			// Don't split when "=" overlaps "!=".
			if t[0] == '=' && token[p-1] == '!' {
				continue
			}
			advance = p
			token = token[:p]
		}
	}
	return advance, token, err
}
