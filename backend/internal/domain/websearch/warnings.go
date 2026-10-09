package websearch

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Warnings attached to a Result. They describe the listing, they don't
// judge it: an accessory can legitimately cost a fraction of the phone it
// fits. The agent and the UI decide what to say.
const (
	// WarnFarBelowOthers: priced under half of what the other listings in
	// the same search cost — the classic shape of a scam listing (or a
	// different, cheaper product that matched the search).
	WarnFarBelowOthers = "price_far_below_others"
	// WarnUnknownStore: a far-below-price listing from a store that isn't a
	// known Indian retailer. Only ever set together with WarnFarBelowOthers;
	// an unfamiliar store on its own is not a red flag.
	WarnUnknownStore = "unknown_store"
)

const (
	// farBelowRatio: a price under this share of the median price of the
	// other listings for the same product is flagged.
	farBelowRatio = 0.5
	// sameProduct: how much two titles must overlap (share of the shorter
	// title's words) to be compared at all. A search for "wireless mouse"
	// returns ₹400 and ₹9,000 mice; only listings of the same product say
	// anything about each other's price.
	sameProduct = 0.7
)

// knownHosts are retailers whose own sites (by registrable domain) are
// recognised; knownNames match the store name a result reports.
var knownHosts = []string{
	"amazon.in", "amazon.com", "flipkart.com", "myntra.com", "ajio.com", "nykaa.com", "tatacliq.com", "croma.com",
	"reliancedigital.in", "vijaysales.com", "blinkit.com", "zeptonow.com", "zepto.com", "swiggy.com", "bigbasket.com",
	"jiomart.com", "meesho.com", "snapdeal.com", "decathlon.in", "apple.com", "samsung.com", "mi.com", "oneplus.in",
	"boat-lifestyle.com", "pharmeasy.in", "apollopharmacy.in", "1mg.com", "netmeds.com", "firstcry.com",
	"pepperfry.com", "ikea.com", "lenskart.com", "bewakoof.com", "hp.com", "dell.com", "lenovo.com", "sony.co.in", "lg.com",
}

var knownNames = []string{
	"amazon", "flipkart", "myntra", "ajio", "nykaa", "tata cliq", "croma", "reliance digital", "vijay sales", "blinkit",
	"zepto", "swiggy", "instamart", "bigbasket", "jiomart", "meesho", "snapdeal", "decathlon", "apple", "samsung",
	"pharmeasy", "apollo", "1mg", "netmeds", "firstcry", "pepperfry", "ikea", "lenskart", "bewakoof",
}

// KnownStore reports whether a result comes from a recognised retailer, by
// its link's domain or the store name it reports.
func KnownStore(r Result) bool {
	if u, err := url.Parse(r.URL); err == nil {
		host := strings.ToLower(u.Hostname())
		for _, h := range knownHosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				return true
			}
		}
	}
	name := strings.ToLower(r.Store)
	for _, n := range knownNames {
		if strings.Contains(name, n) {
			return true
		}
	}
	return false
}

// FlagSuspicious sets Warnings on listings priced far below other listings
// of the same product in the same result set. It modifies results in place
// and returns them.
func FlagSuspicious(results []Result) []Result {
	var priced []int
	words := make([]map[string]bool, len(results))
	sizes := make([]string, len(results))
	for i := range results {
		if results[i].PriceMinorUnits > 0 {
			priced = append(priced, i)
			words[i] = titleWords(results[i].Title)
			sizes[i] = packSize(results[i])
		}
	}
	for _, i := range priced {
		var others []int64
		for _, j := range priced {
			if j != i && overlap(words[i], words[j]) >= sameProduct && samePack(sizes[i], sizes[j]) {
				others = append(others, results[j].PriceMinorUnits)
			}
		}
		if len(others) == 0 || float64(results[i].PriceMinorUnits) >= farBelowRatio*median(others) {
			continue
		}
		results[i].Warnings = appendOnce(results[i].Warnings, WarnFarBelowOthers)
		if !KnownStore(results[i]) {
			results[i].Warnings = appendOnce(results[i].Warnings, WarnUnknownStore)
		}
	}
	return results
}

var numberRE = regexp.MustCompile(`\d+(?:\.\d+)?`)

// packSize is a listing's quantity signature: the numbers in its title and
// variant ("750 ml" → "750", "750 ml x 3" → "3 750"). A single bottle and a
// three-pack share nearly every word of their titles, so without this the
// single bottle reads as "far below" the pack's price.
func packSize(r Result) string {
	nums := numberRE.FindAllString(strings.ToLower(r.Title+" "+r.Snippet), -1)
	seen := map[string]bool{}
	var out []string
	for _, n := range nums {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// samePack compares quantity signatures; a listing that states none can't
// be told apart, so it still compares.
func samePack(a, b string) bool {
	return a == "" || b == "" || a == b
}

func titleWords(title string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(w) >= 2 {
			out[w] = true
		}
	}
	return out
}

// overlap is the share of the shorter title's words the two share.
func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return float64(n) / float64(min(len(a), len(b)))
}

func median(v []int64) float64 {
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return float64(s[n/2])
	}
	return float64(s[n/2-1]+s[n/2]) / 2
}

func appendOnce(list []string, w string) []string {
	for _, x := range list {
		if x == w {
			return list
		}
	}
	return append(list, w)
}
