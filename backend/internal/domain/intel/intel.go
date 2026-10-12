// Package intel is Provider Intelligence: a registry of the paid endpoints the
// catalogs list, what changed about each between refreshes, and how far
// Algebra trusts each one.
//
// Trust is earned from evidence Algebra collected itself, never from what a
// catalog or a provider says about itself:
//
//   - unverified: listed, nothing more is known. The default.
//   - verified: Algebra paid it, it delivered, and the payee it asked for was
//     the payee pinned for it. A fact about past calls, not an endorsement.
//   - blocked: it asked to be paid somewhere other than its pinned payee, or
//     a person blocked it. Nothing is paid to it until a person unblocks it.
//
// The payee of an endpoint is pinned the first time Algebra sees one, in a
// listing or a paid call. A different payee later is the clearest sign that
// a listing was taken over, so it is refused at payment time and blocks the
// endpoint.
package intel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Trust is how far Algebra trusts an endpoint.
type Trust string

const (
	TrustVerified   Trust = "verified"
	TrustUnverified Trust = "unverified"
	TrustBlocked    Trust = "blocked"
)

// Valid reports whether t is one of the three states.
func (t Trust) Valid() bool {
	return t == TrustVerified || t == TrustUnverified || t == TrustBlocked
}

// Status is whether the endpoint is still listed and answering.
type Status string

const (
	StatusActive Status = "active"
	// StatusRemoved: its catalog answered and no longer lists it.
	StatusRemoved Status = "removed"
	// StatusUnreachable: listed, but the last unpaid request couldn't reach it.
	StatusUnreachable Status = "unreachable"
)

// ChangeKind is what changed about an endpoint.
type ChangeKind string

const (
	ChangeAdded           ChangeKind = "added"
	ChangeRemoved         ChangeKind = "removed"
	ChangeRestored        ChangeKind = "restored"
	ChangePriceChanged    ChangeKind = "price_changed"
	ChangePayeeChanged    ChangeKind = "payee_changed"
	ChangeCallableChanged ChangeKind = "callable_changed"
	ChangeUnreachable     ChangeKind = "unreachable"
	ChangeTrust           ChangeKind = "trust_changed"
)

// Reason codes: why an endpoint has its trust, and why a payment was refused.
const (
	// ReasonPayeeChanged: it asked to be paid at an address other than the
	// one pinned for it.
	ReasonPayeeChanged = "PAYEE_CHANGED"
	// ReasonBlocked: the endpoint is blocked; nothing is paid to it.
	ReasonBlocked = "PROVIDER_BLOCKED"
	// ReasonPaidDelivery: a paid call delivered, to the pinned payee.
	ReasonPaidDelivery = "PAID_DELIVERY_MATCHED"
	// ReasonManual: a person decided.
	ReasonManual = "MANUAL"
)

// SuspiciousPriceFactor: a listed price that more than doubles at once is
// flagged for a person to look at (it is still checked at payment time).
const SuspiciousPriceFactor = 2

// Endpoint is one paid endpoint on one network, as the registry knows it.
type Endpoint struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	Capability   string `json:"capability"`
	Method       string `json:"method"`
	URL          string `json:"url"`
	Network      string `json:"network"`
	// Protocol is how it is paid: "x402", or a catalog's own billing.
	Protocol         string `json:"protocol"`
	Asset            string `json:"asset"`
	ListedPriceMinor int64  `json:"listed_price_minor"`
	// PayTo is the payee its listing (or its last quote) named; PinnedPayTo
	// the one Algebra will pay. They differ only for a blocked endpoint.
	PayTo       string `json:"pay_to,omitempty"`
	PinnedPayTo string `json:"pinned_pay_to,omitempty"`
	Callable    bool   `json:"callable"`
	Trust       Trust  `json:"trust"`
	TrustReason string `json:"trust_reason,omitempty"`
	Status      Status `json:"status"`
	// LatencyMS is how long its last paid call took; zero when never paid.
	LatencyMS   int    `json:"latency_ms,omitempty"`
	Fingerprint string `json:"-"`
	// QuotedAt: an unpaid request got payment terms. TestedAt: a paid call
	// delivered. VerifiedAt: when it became verified.
	QuotedAt      *time.Time `json:"quoted_at,omitempty"`
	TestedAt      *time.Time `json:"tested_at,omitempty"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
	FirstSeenAt   time.Time  `json:"first_seen_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	LastChangedAt time.Time  `json:"last_changed_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// Levels separates what is known about an endpoint: a listing is not a
// quote, a quote is not a test, and a test is not verification.
type Levels struct {
	// Listed: a catalog lists it now.
	Listed bool `json:"listed"`
	// Quoted: an unpaid request returned payment terms.
	Quoted bool `json:"quoted"`
	// Tested: Algebra paid it and it delivered.
	Tested bool `json:"tested"`
	// Verified: tested, to the pinned payee, and never since blocked.
	Verified bool `json:"verified"`
	// Executable: Algebra's router can pay and call it today.
	Executable bool `json:"executable"`
}

// Levels reports what is known about e.
func (e Endpoint) Levels() Levels {
	return Levels{
		Listed:     e.Status != StatusRemoved,
		Quoted:     e.QuotedAt != nil,
		Tested:     e.TestedAt != nil,
		Verified:   e.Trust == TrustVerified,
		Executable: e.Callable && e.Status == StatusActive && e.Trust != TrustBlocked && e.Protocol == "x402",
	}
}

// Key is what identifies an endpoint across refreshes: provider, URL (the
// template, before path parameters are filled) and network.
func Key(provider, url, network string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.TrimSpace(url) + "\x00" + strings.ToLower(strings.TrimSpace(network))
}

// ID is the registry's ID for an endpoint.
func ID(provider, url, network string) string {
	sum := sha256.Sum256([]byte(Key(provider, url, network)))
	return "pe_" + hex.EncodeToString(sum[:12])
}

// Listed is what a catalog says about an endpoint right now.
type Listed struct {
	Source       string
	ProviderID   string
	ProviderName string
	Capability   string
	Method       string
	URL          string
	Network      string
	Protocol     string
	Asset        string
	PriceMinor   int64
	PayTo        string
	Callable     bool
}

// ID is the registry ID of the listed endpoint.
func (l Listed) ID() string { return ID(l.ProviderID, l.URL, l.Network) }

func (l Listed) fingerprint() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d|%s|%t|%s", l.Capability, l.Method, l.Protocol, l.Asset, l.PriceMinor, l.PayTo, l.Callable, l.ProviderName)))
	return hex.EncodeToString(sum[:8])
}

// Change is one thing that changed about an endpoint, or one trust decision.
type Change struct {
	ID         string         `json:"id"`
	EndpointID string         `json:"endpoint_id"`
	ProviderID string         `json:"provider_id"`
	Source     string         `json:"source,omitempty"`
	Kind       ChangeKind     `json:"kind"`
	Suspicious bool           `json:"suspicious"`
	Before     map[string]any `json:"before,omitempty"`
	After      map[string]any `json:"after,omitempty"`
	Actor      string         `json:"actor"`
	RunID      string         `json:"run_id,omitempty"`
	At         time.Time      `json:"created_at"`
}

// Merge applies one catalog's fresh listing to what the registry held for
// that catalog. It returns every row to save (each listed endpoint, and each
// one that disappeared) and what changed. Only call it with a listing the
// catalog actually returned: a catalog that couldn't be read says nothing
// about what was removed.
func Merge(prev []Endpoint, listed []Listed, now time.Time) ([]Endpoint, []Change) {
	old := make(map[string]Endpoint, len(prev))
	for _, e := range prev {
		old[e.ID] = e
	}
	var save []Endpoint
	var changes []Change
	seen := map[string]bool{}
	change := func(e Endpoint, kind ChangeKind, suspicious bool, before, after map[string]any) {
		changes = append(changes, Change{EndpointID: e.ID, ProviderID: e.ProviderID, Source: e.Source, Kind: kind,
			Suspicious: suspicious, Before: before, After: after, Actor: "system", At: now})
	}
	for _, l := range listed {
		id := l.ID()
		if seen[id] || l.URL == "" || l.ProviderID == "" {
			continue
		}
		seen[id] = true
		fp := l.fingerprint()
		e, known := old[id]
		if !known {
			e = Endpoint{
				ID: id, Source: l.Source, ProviderID: l.ProviderID, URL: l.URL, Network: l.Network,
				Trust: TrustUnverified, Status: StatusActive, PinnedPayTo: l.PayTo,
				FirstSeenAt: now, LastChangedAt: now,
			}
			fill(&e, l, fp)
			e.LastSeenAt, e.UpdatedAt = now, now
			change(e, ChangeAdded, false, nil, map[string]any{"price_minor": l.PriceMinor, "pay_to": l.PayTo, "network": l.Network})
			save = append(save, e)
			continue
		}
		if e.Status == StatusRemoved {
			e.Status = StatusActive
			change(e, ChangeRestored, false, nil, nil)
		}
		if l.PriceMinor != e.ListedPriceMinor {
			up := e.ListedPriceMinor > 0 && l.PriceMinor > e.ListedPriceMinor*SuspiciousPriceFactor
			change(e, ChangePriceChanged, up, map[string]any{"price_minor": e.ListedPriceMinor}, map[string]any{"price_minor": l.PriceMinor})
		}
		if l.PayTo != "" && l.PayTo != e.PayTo {
			switch {
			case e.PinnedPayTo == "":
				e.PinnedPayTo = l.PayTo
				change(e, ChangePayeeChanged, false, map[string]any{"pay_to": e.PayTo}, map[string]any{"pay_to": l.PayTo, "pinned": true})
			case l.PayTo != e.PinnedPayTo:
				change(e, ChangePayeeChanged, true, map[string]any{"pay_to": e.PayTo, "pinned_pay_to": e.PinnedPayTo}, map[string]any{"pay_to": l.PayTo})
				if e.Trust != TrustBlocked {
					change(e, ChangeTrust, true, map[string]any{"trust": e.Trust}, map[string]any{"trust": TrustBlocked, "reason": ReasonPayeeChanged})
					e.Trust, e.TrustReason, e.VerifiedAt = TrustBlocked, ReasonPayeeChanged, nil
				}
			default:
				// Back to the pinned payee: noted, but it stays as it is.
				change(e, ChangePayeeChanged, false, map[string]any{"pay_to": e.PayTo}, map[string]any{"pay_to": l.PayTo, "pinned": true})
			}
		}
		if l.Callable != e.Callable {
			change(e, ChangeCallableChanged, false, map[string]any{"callable": e.Callable}, map[string]any{"callable": l.Callable})
		}
		if fp != e.Fingerprint {
			e.LastChangedAt = now
		}
		fill(&e, l, fp)
		e.LastSeenAt, e.UpdatedAt = now, now
		save = append(save, e)
	}
	for _, e := range prev {
		if seen[e.ID] || e.Status == StatusRemoved {
			continue
		}
		e.Status, e.LastChangedAt, e.UpdatedAt = StatusRemoved, now, now
		change(e, ChangeRemoved, false, nil, nil)
		save = append(save, e)
	}
	return save, changes
}

func fill(e *Endpoint, l Listed, fp string) {
	e.ProviderName, e.Capability, e.Method, e.Protocol, e.Asset = l.ProviderName, l.Capability, l.Method, l.Protocol, l.Asset
	if e.Protocol == "" {
		e.Protocol = "x402"
	}
	if e.Asset == "" {
		e.Asset = "USDC"
	}
	e.ListedPriceMinor, e.Callable, e.Fingerprint = l.PriceMinor, l.Callable, fp
	if l.PayTo != "" {
		e.PayTo = l.PayTo
	}
}

// Check is the payment-time rule: what, if anything, refuses paying e at
// payTo. It returns a reason code and a sentence for a person, or "".
func (e Endpoint) Check(payTo string) (string, string) {
	if e.Trust == TrustBlocked {
		why := "it is blocked"
		if e.TrustReason != "" {
			why += " (" + e.TrustReason + ")"
		}
		return ReasonBlocked, why
	}
	if e.PinnedPayTo != "" && payTo != "" && payTo != e.PinnedPayTo {
		asked, pinned := Short(payTo), Short(e.PinnedPayTo)
		if asked == pinned {
			asked, pinned = payTo, e.PinnedPayTo
		}
		return ReasonPayeeChanged, fmt.Sprintf("it asks to be paid at %s, not at %s, the payee pinned for it", asked, pinned)
	}
	return "", ""
}

// Short abbreviates an address for a person: "7xKX…AsU".
func Short(a string) string {
	if len(a) <= 12 {
		return a
	}
	return a[:4] + "…" + a[len(a)-4:]
}

// Schedule is when the registry refreshes: every six hours, on the hour, UTC.
const Schedule = "0 */6 * * *"

// NextRun is the first scheduled refresh strictly after t: 00:00, 06:00,
// 12:00 or 18:00 UTC.
func NextRun(t time.Time) time.Time {
	t = t.UTC()
	slot := time.Date(t.Year(), t.Month(), t.Day(), t.Hour()/6*6, 0, 0, 0, time.UTC)
	for !slot.After(t) {
		slot = slot.Add(6 * time.Hour)
	}
	return slot
}

// Quoted records that an unpaid request got payment terms naming payTo.
func (e *Endpoint) Quoted(now time.Time) []Change {
	var out []Change
	if e.Status == StatusUnreachable {
		e.Status = StatusActive
		out = append(out, e.change(ChangeRestored, false, map[string]any{"status": StatusUnreachable}, map[string]any{"status": StatusActive}, "system", now))
	}
	e.QuotedAt, e.UpdatedAt = &now, now
	return out
}

// Unreachable records that an unpaid request couldn't reach it.
func (e *Endpoint) Unreachable(detail string, now time.Time) []Change {
	if e.Status != StatusActive {
		return nil
	}
	e.Status, e.UpdatedAt = StatusUnreachable, now
	return []Change{e.change(ChangeUnreachable, false, nil, map[string]any{"error": detail}, "system", now)}
}

// Flag records a payee that differs from the pinned one: the endpoint is
// blocked until a person looks at it.
func (e *Endpoint) Flag(payTo string, now time.Time) []Change {
	out := []Change{e.change(ChangePayeeChanged, true, map[string]any{"pinned_pay_to": e.PinnedPayTo}, map[string]any{"pay_to": payTo}, "system", now)}
	e.PayTo = payTo
	if e.Trust != TrustBlocked {
		out = append(out, e.change(ChangeTrust, true, map[string]any{"trust": e.Trust}, map[string]any{"trust": TrustBlocked, "reason": ReasonPayeeChanged}, "system", now))
		e.Trust, e.TrustReason, e.VerifiedAt = TrustBlocked, ReasonPayeeChanged, nil
	}
	e.UpdatedAt = now
	return out
}

// Delivered records a paid call that delivered, paid to payTo, in latencyMS.
// The first payee seen is pinned; a delivery to the pinned payee verifies an
// unverified endpoint.
func (e *Endpoint) Delivered(payTo string, latencyMS int, now time.Time) []Change {
	var out []Change
	e.TestedAt, e.UpdatedAt = &now, now
	if latencyMS > 0 {
		e.LatencyMS = latencyMS
	}
	if e.PinnedPayTo == "" && payTo != "" {
		e.PinnedPayTo, e.PayTo = payTo, payTo
		out = append(out, e.change(ChangePayeeChanged, false, nil, map[string]any{"pay_to": payTo, "pinned": true}, "system", now))
	}
	if e.Trust == TrustUnverified && payTo != "" && payTo == e.PinnedPayTo {
		e.Trust, e.TrustReason, e.VerifiedAt = TrustVerified, ReasonPaidDelivery, &now
		out = append(out, e.change(ChangeTrust, false, map[string]any{"trust": TrustUnverified}, map[string]any{"trust": TrustVerified, "reason": ReasonPaidDelivery}, "system", now))
	}
	return out
}

// SetTrust is a person's decision. Unblocking an endpoint blocked for a new
// payee accepts that payee: it becomes the pinned one.
func (e *Endpoint) SetTrust(t Trust, note, actor string, now time.Time) (Change, error) {
	if !t.Valid() {
		return Change{}, fmt.Errorf("trust must be verified, unverified or blocked, not %q", t)
	}
	before := map[string]any{"trust": e.Trust, "reason": e.TrustReason, "pinned_pay_to": e.PinnedPayTo}
	if e.Trust == TrustBlocked && t != TrustBlocked && e.TrustReason == ReasonPayeeChanged && e.PayTo != "" {
		e.PinnedPayTo = e.PayTo
	}
	e.Trust, e.TrustReason, e.UpdatedAt = t, ReasonManual, now
	e.VerifiedAt = nil
	if t == TrustVerified {
		e.VerifiedAt = &now
	}
	after := map[string]any{"trust": t, "reason": ReasonManual, "pinned_pay_to": e.PinnedPayTo}
	if note = strings.TrimSpace(note); note != "" {
		if len(note) > 300 {
			note = note[:300]
		}
		after["note"] = note
	}
	return e.change(ChangeTrust, false, before, after, actor, now), nil
}

func (e Endpoint) change(kind ChangeKind, suspicious bool, before, after map[string]any, actor string, now time.Time) Change {
	return Change{EndpointID: e.ID, ProviderID: e.ProviderID, Source: e.Source, Kind: kind, Suspicious: suspicious,
		Before: before, After: after, Actor: actor, At: now}
}
