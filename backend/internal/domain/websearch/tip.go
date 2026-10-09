package websearch

// Tip is one community-posted deal: a Reddit thread or DesiDime post that
// mentions a price or a code for what the user is shopping for. Unverified
// by definition — no store stands behind it — so it is never a price
// Algebra compares or charges, only a lead the user can check.
type Tip struct {
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	URL     string `json:"url"`
	// Source is where it was posted: "r/dealsforindia", "DesiDime".
	Source string `json:"source"`
	// Code is a coupon code the post shows, if any. It may be expired,
	// single-use or account-bound.
	Code            string `json:"code,omitempty"`
	PriceMinorUnits int64  `json:"price_minor_units,omitempty"`
	// Posted is how old the post is, as the search result showed it.
	Posted string `json:"posted,omitempty"`
}

// CommunitySite is one place community tips are read from, through Google
// search: Filter goes in a site: filter, and every tip's link must be on
// Host (or a subdomain of it) to be kept.
type CommunitySite struct {
	Filter string `json:"filter"`
	Host   string `json:"host"`
	// Path, when set, is the path prefix a tip's link must also have
	// ("/r/dealsforindia"), so a subreddit plugin never returns posts from
	// other subreddits.
	Path string `json:"path,omitempty"`
}
