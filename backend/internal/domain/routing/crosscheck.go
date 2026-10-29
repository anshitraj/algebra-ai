package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Cross-checking: do several providers' answers to one question agree?
//
// Two sellers asked the same thing should say the same thing. When they do, the
// answer is worth more than either alone; when they don't, the person learns it
// before acting on one of them. The comparison reads each answer as JSON and
// compares field by field, only the fields that appear in at least two answers:
// numbers within a tolerance for the kind of work (a token's price may move a
// little between two sources, a balance may not), short text and booleans
// exactly, lists of records matched by what identifies them (an id, a url, a
// symbol). What names the seller or the moment (provider, source, timestamps,
// request ids), what repeats the question, and free text too long to compare are
// left out: they differ between honest providers and say nothing about whether
// they agree.
//
// It claims nothing it didn't check. When no field can be compared the verdict is
// "unclear", not "agree".

// Answer is one provider's response to the question, as the bytes it sent.
type Answer struct {
	Provider string
	Body     []byte
}

// CheckVerdict is what the comparison found.
type CheckVerdict string

const (
	// CheckAgree: every field that could be compared agrees.
	CheckAgree CheckVerdict = "agree"
	// CheckDiffer: at least one field that could be compared does not.
	CheckDiffer CheckVerdict = "differ"
	// CheckUnclear: nothing could be compared, or fewer than two answers were readable.
	CheckUnclear CheckVerdict = "unclear"
)

// Difference is one field the answers do not agree on.
type Difference struct {
	// Path names the field, such as price_usd or results[https://a.example].rank.
	Path string `json:"path"`
	// Values is what each provider said, shortened to fit.
	Values map[string]string `json:"values"`
	// SpreadPct is, for numbers, how far apart the widest two are, as a percent of the larger.
	SpreadPct *float64 `json:"spread_pct,omitempty"`
	// Outlier is the one provider alone against the rest, when three or more answered and one is.
	Outlier string `json:"outlier,omitempty"`
}

// CrossCheck is the result of comparing the answers.
type CrossCheck struct {
	Verdict CheckVerdict `json:"verdict"`
	// Providers are those whose answers were compared.
	Providers []string `json:"providers"`
	// Unreadable are providers whose answer was not JSON, and so was left out.
	Unreadable []string `json:"unreadable,omitempty"`
	// Compared is how many fields were compared, and Agreed how many of those agree.
	Compared int `json:"compared"`
	Agreed   int `json:"agreed"`
	// Differences are the fields that don't agree, the widest first, at most twenty.
	Differences []Difference `json:"differences,omitempty"`
	// MoreDifferences counts the ones not listed.
	MoreDifferences int `json:"more_differences,omitempty"`
	// Outliers are the providers that were alone against the rest on some field.
	Outliers []string `json:"outliers,omitempty"`
	// TolerancePct is how far apart two numbers may be and still agree.
	TolerancePct float64 `json:"tolerance_pct"`
	// Summary says it in a sentence.
	Summary string `json:"summary"`
}

const (
	maxCheckBody        = 1 << 20
	maxCheckLeaves      = 400
	maxCheckDepth       = 8
	maxCheckList        = 50
	maxCheckText        = 120 // longer free text is equal or not comparable, never "different"
	maxCheckDifferences = 20
	maxShownValue       = 60

	defaultCheckTolerance = 0.02
)

// checkTolerance is how far apart two numbers may be, as a fraction of the
// larger, for each kind of work. Prices move between sources; a balance is
// the same everywhere or someone is wrong.
var checkTolerance = map[string]float64{
	"token.price":      0.01,
	"wallet.balances":  0.001,
	"weather.forecast": 0.10,
}

// CheckToleranceFor is the tolerance for numbers in an answer to capability.
func CheckToleranceFor(capability string) float64 {
	if t, ok := checkTolerance[capability]; ok {
		return t
	}
	return defaultCheckTolerance
}

// skippedKeys name the seller or the moment, repeat the question, or are labels
// that honest providers disagree on. Compared on a normalised key: lower case,
// letters and digits only.
var skippedKeys = map[string]bool{
	"provider": true, "providers": true, "source": true, "sources": true, "attribution": true, "license": true,
	"asof": true, "timestamp": true, "time": true, "ts": true, "date": true, "updatedat": true, "generatedat": true,
	"fetchedat": true, "retrievedat": true, "createdat": true, "lastupdated": true,
	"requestid": true, "traceid": true, "id": true, "uuid": true, "requestedat": true,
	"note": true, "notes": true, "disclaimer": true, "terms": true, "demo": true, "sandbox": true, "test": true,
	"latency": true, "latencyms": true, "elapsed": true, "elapsedms": true, "cached": true, "cache": true, "version": true,
	"url": true, "link": true, "href": true,
}

// identityKeys name a record in a list, in the order tried.
var identityKeys = []string{"id", "url", "symbol", "name", "slug", "address", "mint", "key", "code", "title"}

type leafKind uint8

const (
	leafNumber leafKind = iota + 1
	leafText
	leafBool
	leafNull
)

type leaf struct {
	kind leafKind
	num  float64
	text string
}

type fieldView struct {
	provider string
	l        leaf
}

// CompareAnswers compares the providers' answers to one question. inputKeys are
// the request's own top-level parameter names: an answer that repeats them
// (the mint asked about, the query) says nothing about agreement.
func CompareAnswers(capability string, inputKeys []string, answers []Answer) CrossCheck {
	tol := CheckToleranceFor(capability)
	out := CrossCheck{TolerancePct: round2(tol * 100), Providers: []string{}}
	echo := map[string]bool{}
	for _, k := range inputKeys {
		echo[normKey(k)] = true
	}

	type flat struct {
		provider string
		leaves   map[string]leaf
	}
	var readable []flat
	for _, a := range answers {
		leaves, ok := flatten(a.Body, echo)
		if !ok {
			out.Unreadable = append(out.Unreadable, a.Provider)
			continue
		}
		readable = append(readable, flat{a.Provider, leaves})
		out.Providers = append(out.Providers, a.Provider)
	}
	if len(readable) < 2 {
		out.Verdict = CheckUnclear
		out.Summary = "Fewer than two of the answers could be read as JSON, so there is nothing to compare."
		return out
	}

	byPath := map[string][]fieldView{}
	for _, f := range readable {
		for path, l := range f.leaves {
			byPath[path] = append(byPath[path], fieldView{f.provider, l})
		}
	}
	paths := make([]string, 0, len(byPath))
	for p, views := range byPath {
		if len(views) >= 2 {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	var diffs []Difference
	numeric := false
	for _, p := range paths {
		views := byPath[p]
		verdict, d := compareField(p, views, tol)
		switch verdict {
		case fieldSkipped:
		case fieldAgree:
			out.Compared++
			out.Agreed++
			if allNumeric(views) {
				numeric = true
			}
		case fieldDiffer:
			out.Compared++
			if allNumeric(views) {
				numeric = true
			}
			diffs = append(diffs, d)
		}
	}
	// The widest first, then by name, so the same answers read the same way twice.
	sort.SliceStable(diffs, func(i, j int) bool {
		a, b := 0.0, 0.0
		if diffs[i].SpreadPct != nil {
			a = *diffs[i].SpreadPct
		}
		if diffs[j].SpreadPct != nil {
			b = *diffs[j].SpreadPct
		}
		if a != b {
			return a > b
		}
		return diffs[i].Path < diffs[j].Path
	})
	if len(diffs) > maxCheckDifferences {
		out.MoreDifferences = len(diffs) - maxCheckDifferences
		diffs = diffs[:maxCheckDifferences]
	}
	out.Differences = diffs

	seen := map[string]bool{}
	for _, d := range diffs {
		if d.Outlier != "" && !seen[d.Outlier] {
			seen[d.Outlier] = true
			out.Outliers = append(out.Outliers, d.Outlier)
		}
	}
	sort.Strings(out.Outliers)

	switch {
	case out.Compared == 0:
		out.Verdict = CheckUnclear
	case len(diffs) == 0 && out.MoreDifferences == 0:
		out.Verdict = CheckAgree
	default:
		out.Verdict = CheckDiffer
	}
	out.Summary = summarize(out, numeric)
	return out
}

type fieldVerdict uint8

const (
	fieldSkipped fieldVerdict = iota
	fieldAgree
	fieldDiffer
)

func compareField(path string, views []fieldView, tol float64) (fieldVerdict, Difference) {
	d := Difference{Path: path, Values: map[string]string{}}
	for _, v := range views {
		d.Values[v.provider] = shown(v.l)
	}

	if allNumeric(views) {
		nums := make([]pn, len(views))
		for i, v := range views {
			n, _ := asNumber(v.l)
			nums[i] = pn{v.provider, n}
		}
		spread := spreadOf(nums)
		if spread <= tol {
			return fieldAgree, d
		}
		pct := round2(spread * 100)
		d.SpreadPct = &pct
		d.Outlier = numericOutlier(nums, tol)
		return fieldDiffer, d
	}

	first := views[0].l
	same := true
	for _, v := range views[1:] {
		if v.l.kind != first.kind || (v.l.kind != leafNull && normText(v.l) != normText(first)) {
			same = false
		}
	}
	if same {
		return fieldAgree, d
	}
	// Two long passages of prose are not "different" in the way two prices are.
	for _, v := range views {
		if v.l.kind == leafText && len(v.l.text) > maxCheckText {
			return fieldSkipped, d
		}
	}
	d.Outlier = majorityOutlier(views)
	return fieldDiffer, d
}

type pn struct {
	provider string
	n        float64
}

// spreadOf is the gap between the largest and smallest, as a fraction of the larger magnitude.
func spreadOf(v []pn) float64 {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, x := range v {
		lo, hi = math.Min(lo, x.n), math.Max(hi, x.n)
	}
	scale := math.Max(math.Abs(lo), math.Abs(hi))
	if scale < 1e-12 {
		return 0
	}
	return (hi - lo) / scale
}

// numericOutlier is the one provider whose removal leaves the rest in agreement,
// when there are three or more and exactly one such provider.
func numericOutlier(v []pn, tol float64) string {
	if len(v) < 3 {
		return ""
	}
	who, n := "", 0
	for i := range v {
		rest := make([]pn, 0, len(v)-1)
		rest = append(rest, v[:i]...)
		rest = append(rest, v[i+1:]...)
		if spreadOf(rest) <= tol {
			who, n = v[i].provider, n+1
		}
	}
	if n == 1 {
		return who
	}
	return ""
}

// majorityOutlier is the one provider outside a strict majority that agrees.
func majorityOutlier(views []fieldView) string {
	if len(views) < 3 {
		return ""
	}
	groups := map[string][]string{}
	for _, v := range views {
		k := fmt.Sprintf("%d|%s", v.l.kind, normText(v.l))
		groups[k] = append(groups[k], v.provider)
	}
	for _, members := range groups {
		if len(members)*2 > len(views) {
			if len(views)-len(members) != 1 {
				return ""
			}
			in := map[string]bool{}
			for _, m := range members {
				in[m] = true
			}
			for _, v := range views {
				if !in[v.provider] {
					return v.provider
				}
			}
		}
	}
	return ""
}

func allNumeric(views []fieldView) bool {
	for _, v := range views {
		if _, ok := asNumber(v.l); !ok {
			return false
		}
	}
	return true
}

// asNumber reads a number, or a string that is one ("186.20", "1,204.5"): many
// providers send prices as text.
func asNumber(l leaf) (float64, bool) {
	switch l.kind {
	case leafNumber:
		return l.num, true
	case leafText:
		s := strings.ReplaceAll(strings.TrimSpace(l.text), ",", "")
		if s == "" || len(s) > 32 {
			return 0, false
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

func normText(l leaf) string {
	switch l.kind {
	case leafNull:
		return "null"
	case leafBool, leafText:
		return strings.Join(strings.Fields(strings.ToLower(l.text)), " ")
	case leafNumber:
		return strconv.FormatFloat(l.num, 'g', -1, 64)
	}
	return ""
}

func shown(l leaf) string {
	var s string
	switch l.kind {
	case leafNumber:
		s = strconv.FormatFloat(l.num, 'g', 10, 64)
	case leafNull:
		s = "null"
	default:
		s = l.text
	}
	if len(s) > maxShownValue {
		s = s[:maxShownValue-1] + "…"
	}
	return s
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func normKey(k string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(k) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// flatten reads a JSON answer into its leaves by path. ok is false when the body
// is not JSON, or is too big to compare.
func flatten(body []byte, echo map[string]bool) (map[string]leaf, bool) {
	if len(body) == 0 || len(body) > maxCheckBody {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	switch v.(type) {
	case map[string]any, []any:
	default:
		return nil, false
	}
	f := &flattener{echo: echo, out: map[string]leaf{}}
	f.walk(v, "", 0, "")
	return f.out, true
}

type flattener struct {
	echo map[string]bool
	out  map[string]leaf
}

func (f *flattener) add(path string, l leaf) {
	if path == "" || len(f.out) >= maxCheckLeaves {
		return
	}
	f.out[path] = l
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// walk adds the leaves under v, leaving out the fields that say nothing about
// agreement: the request's own parameters repeated back, and skip, a key not to add
// (a record's identity, which already named it).
func (f *flattener) walk(v any, path string, depth int, skip string) {
	if depth > maxCheckDepth {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			nk := normKey(k)
			if nk == "" || skippedKeys[nk] || f.echo[nk] || (skip != "" && nk == normKey(skip)) {
				continue
			}
			f.walk(x[k], join(path, k), depth+1, "")
		}
	case []any:
		if len(x) > maxCheckList {
			x = x[:maxCheckList]
		}
		if key, ok := identityOf(x); ok {
			for _, it := range x {
				m := it.(map[string]any)
				id, _ := scalarText(lookup(m, key))
				f.walk(it, path+"["+id+"]", depth+1, key)
			}
			return
		}
		if allScalar(x) {
			parts := make([]string, 0, len(x))
			for _, it := range x {
				s, _ := scalarText(it)
				parts = append(parts, s)
			}
			sort.Strings(parts)
			f.add(path, leaf{kind: leafText, text: strings.Join(parts, " | ")})
			return
		}
		for i, it := range x {
			f.walk(it, fmt.Sprintf("%s[%d]", path, i), depth+1, "")
		}
	case json.Number:
		if n, err := x.Float64(); err == nil && !math.IsInf(n, 0) {
			f.add(path, leaf{kind: leafNumber, num: n})
		} else {
			f.add(path, leaf{kind: leafText, text: x.String()})
		}
	case string:
		f.add(path, leaf{kind: leafText, text: x})
	case bool:
		f.add(path, leaf{kind: leafBool, text: strconv.FormatBool(x)})
	case nil:
		f.add(path, leaf{kind: leafNull})
	}
}

func lookup(m map[string]any, key string) any {
	for k, v := range m {
		if normKey(k) == normKey(key) {
			return v
		}
	}
	return nil
}

func scalarText(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		return strconv.FormatBool(x), true
	}
	return "", false
}

func allScalar(items []any) bool {
	for _, it := range items {
		if _, ok := scalarText(it); !ok {
			return false
		}
	}
	return len(items) > 0
}

// identityOf is the key that names every record in a list, when one does and no
// two records share it.
func identityOf(items []any) (string, bool) {
	if len(items) == 0 {
		return "", false
	}
	for _, key := range identityKeys {
		seen := map[string]bool{}
		ok := true
		for _, it := range items {
			m, isMap := it.(map[string]any)
			if !isMap {
				return "", false
			}
			s, good := scalarText(lookup(m, key))
			if !good || s == "" || seen[s] {
				ok = false
				break
			}
			seen[s] = true
		}
		if ok {
			return key, true
		}
	}
	return "", false
}

func summarize(c CrossCheck, numeric bool) string {
	n := len(c.Providers)
	switch c.Verdict {
	case CheckUnclear:
		return "Nothing in these answers could be compared field by field: no field appears in more than one of them, or what does is free text."
	case CheckAgree:
		s := fmt.Sprintf("All %d providers agree on the %d fields they could be compared on", n, c.Compared)
		if numeric {
			s += fmt.Sprintf(" (numbers within %s%%)", trimPct(c.TolerancePct))
		}
		return s + "."
	}
	listed := c.Differences
	if len(listed) > 3 {
		listed = listed[:3]
	}
	parts := make([]string, 0, len(listed))
	for _, d := range listed {
		parts = append(parts, describeDifference(d))
	}
	s := fmt.Sprintf("%d providers differ on %d of the %d fields compared: %s", n, len(c.Differences)+c.MoreDifferences, c.Compared, strings.Join(parts, "; "))
	if len(c.Differences) > len(listed) || c.MoreDifferences > 0 {
		s += "; and more"
	}
	s += "."
	if len(c.Outliers) == 1 {
		alone := true
		for _, d := range c.Differences {
			if d.Outlier != c.Outliers[0] {
				alone = false
			}
		}
		if alone {
			s += fmt.Sprintf(" %s is the odd one out.", c.Outliers[0])
		}
	}
	return s
}

func describeDifference(d Difference) string {
	providers := make([]string, 0, len(d.Values))
	for p := range d.Values {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	vals := make([]string, 0, len(providers))
	for _, p := range providers {
		vals = append(vals, d.Values[p])
	}
	s := fmt.Sprintf("%s (%s", d.Path, strings.Join(vals, " vs "))
	if d.SpreadPct != nil {
		s += fmt.Sprintf(", %s%% apart", trimPct(*d.SpreadPct))
	}
	return s + ")"
}

func trimPct(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
