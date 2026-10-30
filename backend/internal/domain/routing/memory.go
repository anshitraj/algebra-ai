package routing

import "time"

// freshness is how long an accepted answer to each kind of work stays good
// enough to hand back again instead of paying for it twice. Prices and
// balances move by the minute; a domain's records or an IP's location barely
// move in a day. A model's completion is never reused: asking again is asking
// for another answer.
var freshness = map[string]time.Duration{
	"token.price":       time.Minute,
	"wallet.balances":   30 * time.Second,
	"solana.token-risk": 30 * time.Minute,
	"wallet.risk":       30 * time.Minute,
	"web.search":        30 * time.Minute,
	"web.scrape":        30 * time.Minute,
	"news.search":       10 * time.Minute,
	"weather.forecast":  30 * time.Minute,
	"text.translate":    24 * time.Hour,
	"ip.geolocate":      24 * time.Hour,
	"domain.lookup":     24 * time.Hour,
	"email.verify":      24 * time.Hour,
	"llm.chat":          0,
	"image.generate":    0,
}

// FreshFor is how long an accepted answer to capability may be reused: its
// class's default, or zero (never) for work Algebra has no class for, whose
// freshness it can't know. A request that states its own maximum age says
// more than this and wins.
func FreshFor(capability string) time.Duration { return freshness[capability] }
