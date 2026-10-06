package routing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// A Class is one kind of work that many providers sell under different names:
// "the current price of a Solana token" is Birdeye's /defi/price, Alchemy's
// /prices/v1/tokens/by-address and a dozen PayAI listings. Catalogs list each
// as its own capability ("birdeye.data.get.x402-defi-price"), so an agent that
// names one can never be routed to another. A Class gives them one name
// ("token.price") and one input shape, so the router can compare every
// provider that does the work and fall back between them.
//
// Membership is decided from what a catalog says about an endpoint (its path
// and description), which is third-party text: a match makes an endpoint a
// candidate, never a trusted one. It is still priced with an unpaid request,
// screened by policy and judged after delivery like any other.
type Class struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Kind        Kind         `json:"kind"`
	Fields      []ClassField `json:"fields"`
	// Sample is a canonical input the free health probe asks with, so the
	// 402 a provider answers is the one a real call would get.
	Sample json.RawMessage `json:"sample"`

	match   *regexp.Regexp
	exclude *regexp.Regexp
	// solana: the class is about Solana, so an endpoint whose description
	// names only EVM chains is not a member, whatever its path says.
	solana bool
	// fixed are provider parameters with one right value for this class
	// ("chain": "solana"), filled in when an endpoint takes them.
	fixed map[string]string
}

// ClassField is one input field of a class, under its canonical name.
type ClassField struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Example     string `json:"example,omitempty"`
	// synonyms are the parameter names providers use for it.
	synonyms []string
}

const (
	usdcMint  = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	sampleKey = "vines1vzrYbzLMRdu58ou5XTby4qAqVRLmqo36NKPTg" // a well-known public wallet
)

var (
	evmRE    = regexp.MustCompile(`erc-?20|\bevm\b|ethereum|\beth\b|polygon|arbitrum|\bbase\b`)
	solanaRE = regexp.MustCompile(`solana|\bspl\b|\bsol\b|pump\.?fun|\bmint\b`)
)

var solanaChain = map[string]string{"chain": "solana", "network": "solana", "blockchain": "solana"}

var mintSynonyms = []string{"mint", "address", "token", "tokenaddress", "token_address", "mintaddress", "addr", "ca", "contract", "contractaddress", "tokenmint"}

// classes is the taxonomy, most specific first: when an endpoint matches two,
// the earlier one wins a tie.
var classes = []Class{
	{
		ID: "solana.token-risk", Title: "Token risk check", Kind: KindData,
		Description: "A safety screen of one SPL token mint: rug-pull, honeypot, authority and holder-concentration risks.",
		Fields:      []ClassField{{Name: "mint", Required: true, Description: "SPL token mint address", Example: usdcMint, synonyms: mintSynonyms}},
		Sample:      json.RawMessage(`{"mint":"` + usdcMint + `"}`),
		match:       regexp.MustCompile(`rug[-_ ]?pull|honeypot|token[-_ /]?(risk|security|safety|verdict|checkup|check|guard|scan)|risk[-_ ]?screen|tokenguard|sol-token-(safety|report)|token safety`),
		exclude:     regexp.MustCompile(`drug|screener|recent|batch|launches|graduation|validator|protocol|mcp|manifest|historical|rwa`),
		solana:      true,
		fixed:       solanaChain,
	},
	{
		ID: "token.price", Title: "Token price", Kind: KindData,
		Description: "The current USD price of one Solana token, by mint address.",
		Fields:      []ClassField{{Name: "mint", Required: true, Description: "SPL token mint address", Example: usdcMint, synonyms: mintSynonyms}},
		Sample:      json.RawMessage(`{"mint":"` + usdcMint + `"}`),
		match:       regexp.MustCompile(`(token|defi|coin|spot)[-_ /]?price\b|/price$|price/token|tokens/by-address|real-time price|current price|spot price|latest price`),
		exclude:     regexp.MustCompile(`histor|chart|candle|ohlc|change|stats|polymarket|forex|stock|product|gas|nft|dispersion|positioning|drop|fair|gap|timestamp|unix|denomination|feeds|symbol|prediction|volume`),
		solana:      true,
		fixed:       solanaChain,
	},
	{
		ID: "wallet.balances", Title: "Wallet balances", Kind: KindData,
		Description: "The token balances held by one Solana wallet.",
		Fields: []ClassField{{Name: "wallet", Required: true, Description: "Wallet address", Example: sampleKey,
			synonyms: []string{"wallet", "address", "owner", "walletaddress", "wallet_address", "account", "owneraddress", "pubkey"}}},
		Sample:  json.RawMessage(`{"wallet":"` + sampleKey + `"}`),
		match:   regexp.MustCompile(`(wallet|address|account)[-_ /]?(balance|balances|holdings|portfolio|tokens|assets)|token[-_ ]balances|wallet portfolio`),
		exclude: regexp.MustCompile(`polymarket|nft|histor|time-series|pnl|entity|batch|holder|risk|safety`),
		solana:  true,
		fixed:   solanaChain,
	},
	{
		ID: "wallet.risk", Title: "Wallet risk screen", Kind: KindData,
		Description: "A sanctions, AML or fraud screen of one wallet address.",
		Fields: []ClassField{{Name: "wallet", Required: true, Description: "Wallet address", Example: sampleKey,
			synonyms: []string{"wallet", "address", "addr", "walletaddress", "wallet_address", "account"}}},
		Sample:  json.RawMessage(`{"wallet":"` + sampleKey + `"}`),
		match:   regexp.MustCompile(`(wallet|address)[-_ /]?(risk|screen|sanction|aml|score|reputation)|sanction|\baml\b`),
		exclude: regexp.MustCompile(`token|batch|polymarket|investigat`),
		solana:  true,
		fixed:   solanaChain,
	},
	{
		ID: "web.search", Title: "Web search", Kind: KindData,
		Description: "Ranked web results for a free-text query.",
		Fields: []ClassField{{Name: "query", Required: true, Description: "What to search for", Example: "solana payment channels",
			synonyms: []string{"query", "q", "search", "searchterm", "keyword", "keywords", "text"}}},
		Sample:  json.RawMessage(`{"query":"solana"}`),
		match:   regexp.MustCompile(`web[-_ /]?search|search the web|\bserp\b|google search|search engine|internet search`),
		exclude: regexp.MustCompile(`twitter|tweet|reddit|github|amazon|token|pool|flight|restaurant|people|person|product|job|hotel|image|news|rank-check|organic position`),
	},
	{
		ID: "web.scrape", Title: "Web page extract", Kind: KindData,
		Description: "The readable content of one web page, as text or markdown.",
		Fields: []ClassField{{Name: "url", Required: true, Description: "Page to read", Example: "https://solana.com",
			synonyms: []string{"url", "link", "page", "target", "pageurl", "page_url", "website"}}},
		Sample:  json.RawMessage(`{"url":"https://example.com"}`),
		match:   regexp.MustCompile(`scrape|crawl|to-markdown|webpage|web page|web/fetch|read[-_ ]?page|reader[-_ ]mode|url to (markdown|text)`),
		exclude: regexp.MustCompile(`pdf|robots|llms|audit|search|crawlers|sitemap|screenshot|png`),
	},
	{
		ID: "news.search", Title: "News search", Kind: KindData,
		Description: "Recent news articles for a query.",
		Fields: []ClassField{{Name: "query", Required: true, Description: "Topic to search news for", Example: "solana",
			synonyms: []string{"query", "q", "topic", "keyword", "keywords", "search"}}},
		Sample:  json.RawMessage(`{"query":"solana"}`),
		match:   regexp.MustCompile(`\bnews\b`),
		exclude: regexp.MustCompile(`newsletter|sentiment|summary of`),
	},
	{
		ID: "llm.chat", Title: "LLM completion", Kind: KindInference,
		Description: "A language-model answer to a prompt.",
		Fields: []ClassField{{Name: "prompt", Required: true, Description: "The prompt", Example: "Say hello",
			synonyms: []string{"prompt", "message", "input", "question", "text", "query"}}},
		Sample:  json.RawMessage(`{"prompt":"ping"}`),
		match:   regexp.MustCompile(`chat/completions|/completions?\b|/chat\b|/llm\b|/responses$|language model|\binference\b|ask[-_ ](ai|model|llm)`),
		exclude: regexp.MustCompile(`image|video|audio|embedding|tts|speech|vision|summar|translat|compress|chunk|token[-_ ]?count|count exact|statistic|grounding|context|wallet|search`),
	},
	{
		ID: "image.generate", Title: "Image generation", Kind: KindInference,
		Description: "An image generated from a text prompt.",
		Fields: []ClassField{{Name: "prompt", Required: true, Description: "What to draw", Example: "a lighthouse at dusk",
			synonyms: []string{"prompt", "text", "description", "input"}}},
		Sample:  json.RawMessage(`{"prompt":"a lighthouse"}`),
		match:   regexp.MustCompile(`image[-_ /]?(gen|generation|generate|create)|text[-_ ]to[-_ ]image|txt2img|generate (an )?images?|images/generations`),
		exclude: regexp.MustCompile(`video|edit|upscale|caption|describe|analy|ocr|background`),
	},
	{
		ID: "weather.forecast", Title: "Weather", Kind: KindData,
		Description: "Current weather and forecast for a place.",
		Fields: []ClassField{{Name: "location", Required: true, Description: "City or place name", Example: "Lisbon",
			synonyms: []string{"location", "city", "q", "place", "query", "loc"}}},
		Sample:  json.RawMessage(`{"location":"Lisbon"}`),
		match:   regexp.MustCompile(`weather|forecast`),
		exclude: regexp.MustCompile(`gas|air[-_ ]?quality|marine|econ|taf|metar|airport|hourly|price|sales|demand|token`),
	},
	{
		ID: "text.translate", Title: "Translation", Kind: KindInference,
		Description: "Text translated into another language.",
		Fields: []ClassField{
			{Name: "text", Required: true, Description: "Text to translate", Example: "hello", synonyms: []string{"text", "q", "input", "content", "source"}},
			{Name: "target", Required: true, Description: "Target language code", Example: "es", synonyms: []string{"target", "to", "targetlang", "target_lang", "targetlanguage", "target_language", "lang", "language"}},
		},
		Sample: json.RawMessage(`{"target":"es","text":"hello"}`),
		match:  regexp.MustCompile(`translat`),
	},
	{
		ID: "ip.geolocate", Title: "IP geolocation", Kind: KindData,
		Description: "Where an IP address is.",
		Fields: []ClassField{{Name: "ip", Required: true, Description: "IPv4 or IPv6 address", Example: "8.8.8.8",
			synonyms: []string{"ip", "ipaddress", "ip_address", "address", "query"}}},
		Sample: json.RawMessage(`{"ip":"8.8.8.8"}`),
		match:  regexp.MustCompile(`\bip[-_ /]?(geo|lookup|location|info|intel)|geoip|geolocat`),
	},
	{
		ID: "domain.lookup", Title: "Domain lookup", Kind: KindData,
		Description: "WHOIS, DNS or availability for a domain name.",
		Fields: []ClassField{{Name: "domain", Required: true, Description: "Domain name", Example: "solana.com",
			synonyms: []string{"domain", "name", "hostname", "host", "query", "q"}}},
		Sample:  json.RawMessage(`{"domain":"solana.com"}`),
		match:   regexp.MustCompile(`whois|\bdns\b|domain[-_ ]?(lookup|info|check|availability|search)`),
		exclude: regexp.MustCompile(`email|audit|aeo`),
	},
	{
		ID: "email.verify", Title: "Email verification", Kind: KindData,
		Description: "Whether an email address exists and accepts mail.",
		Fields: []ClassField{{Name: "email", Required: true, Description: "Email address", Example: "hello@example.com",
			synonyms: []string{"email", "address", "mail", "emailaddress", "email_address"}}},
		Sample: json.RawMessage(`{"email":"hello@example.com"}`),
		match:  regexp.MustCompile(`email[-_ ]?(verif|valid|check|deliverab)`),
	},
}

// Classes returns the taxonomy.
func Classes() []Class { return slices.Clone(classes) }

// ClassByID finds a class by its capability ID.
func ClassByID(id string) (Class, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, c := range classes {
		if c.ID == id {
			return c, true
		}
	}
	return Class{}, false
}

// Classify names the class an endpoint belongs to, from the catalog's path and
// description for it. A match in the path counts more than one in the
// description, which is free text; an exclusion anywhere rules a class out.
// It returns false when nothing matches.
func Classify(path, description string) (string, bool) {
	p, d := strings.ToLower(path), strings.ToLower(description)
	evmOnly := evmRE.MatchString(d) && !solanaRE.MatchString(d+" "+p)
	best, bestScore := "", 0
	for _, c := range classes {
		if c.exclude != nil && (c.exclude.MatchString(p) || c.exclude.MatchString(d)) {
			continue
		}
		if c.solana && evmOnly {
			continue
		}
		score := 0
		if c.match.MatchString(p) {
			score += 2
		}
		if c.match.MatchString(d) {
			score++
		}
		if score > bestScore {
			best, bestScore = c.ID, score
		}
	}
	return best, bestScore > 0
}

// InputAdapter turns a class's canonical input into one provider's: it renames
// fields to the provider's parameter names and adds parameters that have one
// right value for the class. It is derived from what the provider publishes,
// so it is the same every time for the same endpoint.
type InputAdapter struct {
	// Rename maps a canonical field to the provider's name for it.
	Rename map[string]string `json:"rename,omitempty"`
	// Fixed are provider parameters with constant values.
	Fixed map[string]string `json:"fixed,omitempty"`
	// Template, when set, is the provider's whole input with "{{field}}"
	// string placeholders for canonical fields, for providers that nest
	// their parameters ({"addresses":[{"address":"{{mint}}"}]}). It wins over
	// Rename and Fixed.
	Template json.RawMessage `json:"template,omitempty"`
}

// maxAdapterEntries bounds an adapter: a class has a handful of fields.
const maxAdapterEntries = 16

// Validate refuses an adapter that couldn't have come from a class.
func (a *InputAdapter) Validate() error {
	if a == nil {
		return nil
	}
	if len(a.Rename)+len(a.Fixed) > maxAdapterEntries {
		return errors.New("an input adapter has too many entries")
	}
	for k, v := range a.Rename {
		if k == "" || v == "" || len(k) > 64 || len(v) > 64 {
			return errors.New("an input adapter renames to or from an empty or overlong name")
		}
	}
	for k, v := range a.Fixed {
		if k == "" || len(k) > 64 || len(v) > 128 {
			return errors.New("an input adapter fixes an empty or overlong parameter")
		}
	}
	if len(a.Template) > 0 && (len(a.Template) > 4<<10 || !json.Valid(a.Template)) {
		return errors.New("an input adapter's template is not small, valid JSON")
	}
	return nil
}

// Apply builds the provider's input from the canonical one. A canonical field
// the provider has no name for is left out: sending a parameter it doesn't
// know could change what it does.
func (a *InputAdapter) Apply(input json.RawMessage) (json.RawMessage, error) {
	if a == nil {
		return input, nil
	}
	var in map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	if err := dec.Decode(&in); err != nil {
		return nil, errors.New("the input must be a JSON object")
	}
	if len(a.Template) > 0 {
		return fillTemplate(a.Template, in)
	}
	out := make(map[string]json.RawMessage, len(in)+len(a.Fixed))
	for k, v := range a.Fixed {
		b, _ := json.Marshal(v)
		out[k] = b
	}
	for canon, v := range in {
		if to, ok := a.Rename[canon]; ok {
			out[to] = v
		}
	}
	return json.Marshal(out)
}

// fillTemplate replaces every string in tpl that is exactly "{{field}}" with
// that canonical field's value. A placeholder for a field the input lacks is
// an error, not an empty string: a provider asked about "" answers about the
// wrong thing.
func fillTemplate(tpl json.RawMessage, in map[string]json.RawMessage) (json.RawMessage, error) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(tpl))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, errors.New("the input template is not valid JSON")
	}
	var walk func(v any) (any, error)
	walk = func(v any) (any, error) {
		switch t := v.(type) {
		case string:
			if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") {
				name := strings.TrimSpace(t[2 : len(t)-2])
				raw, ok := in[name]
				if !ok {
					return nil, fmt.Errorf("the input has no %q", name)
				}
				return raw, nil
			}
			return t, nil
		case []any:
			for i := range t {
				var err error
				if t[i], err = walk(t[i]); err != nil {
					return nil, err
				}
			}
			return t, nil
		case map[string]any:
			for k := range t {
				var err error
				if t[k], err = walk(t[k]); err != nil {
					return nil, err
				}
			}
			return t, nil
		}
		return v, nil
	}
	filled, err := walk(doc)
	if err != nil {
		return nil, err
	}
	return json.Marshal(filled)
}

// Adapt builds the adapter for an endpoint whose parameters are known by
// name. required are the parameters the endpoint says it needs; every one of
// them must be something the class can fill, and every required class field
// must find a parameter, or the endpoint can't be called with the class's
// input and ok is false.
func (c Class) Adapt(params, required []string) (*InputAdapter, bool) {
	norm := func(s string) string {
		return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(s)))
	}
	byNorm := map[string]string{}
	for _, p := range params {
		if _, dup := byNorm[norm(p)]; !dup {
			byNorm[norm(p)] = p
		}
	}
	a := &InputAdapter{Rename: map[string]string{}, Fixed: map[string]string{}}
	used := map[string]bool{}
	for _, f := range c.Fields {
		found := false
		for _, syn := range f.synonyms {
			if p, ok := byNorm[norm(syn)]; ok && !used[p] {
				a.Rename[f.Name], used[p], found = p, true, true
				break
			}
		}
		if !found && f.Required {
			return nil, false
		}
	}
	for k, v := range c.fixed {
		if p, ok := byNorm[norm(k)]; ok && !used[p] {
			a.Fixed[p], used[p] = v, true
		}
	}
	for _, r := range required {
		if p, ok := byNorm[norm(r)]; !ok || !used[p] {
			return nil, false
		}
	}
	if len(a.Fixed) == 0 {
		a.Fixed = nil
	}
	return a, true
}

// Canonical is the adapter for an endpoint that takes the class's own field
// names, as Algebra's own and configured providers do.
func (c Class) Canonical() *InputAdapter {
	a := &InputAdapter{Rename: map[string]string{}}
	for _, f := range c.Fields {
		a.Rename[f.Name] = f.Name
	}
	return a
}

// CheckInput reports what is wrong with an input for this class: it must be
// a JSON object with every required field as a non-empty string.
func (c Class) CheckInput(input json.RawMessage) error {
	var in map[string]any
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("%s takes a JSON object", c.ID)
	}
	var missing []string
	for _, f := range c.Fields {
		s, ok := in[f.Name].(string)
		if f.Required && (!ok || strings.TrimSpace(s) == "") {
			missing = append(missing, f.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s needs %s (e.g. %s)", c.ID, strings.Join(missing, ", "), string(c.Sample))
	}
	return nil
}

// Fixed returns the class's constant provider parameters.
func (c Class) Fixed() map[string]string { return maps.Clone(c.fixed) }
