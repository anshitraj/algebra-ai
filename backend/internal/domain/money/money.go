// Package money provides a minor-units currency type shared by every domain
// package that talks about prices. Amounts are always integers (e.g. paise
// for INR, cents for USD) — floats are never used for money anywhere in
// Algebra, because rounding drift on a payment amount is a financial bug.
package money

import "fmt"

// Amount is a currency amount expressed in the smallest unit of Currency
// (e.g. paise for INR, cents for USD).
type Amount struct {
	MinorUnits int64  `json:"minor_units"`
	Currency   string `json:"currency"`
}

// Zero reports whether the amount is exactly zero (currency-agnostic).
func (a Amount) Zero() bool {
	return a.MinorUnits == 0
}

// SameCurrency reports whether two amounts share a currency.
func (a Amount) SameCurrency(b Amount) bool {
	return a.Currency == b.Currency
}

// Add returns a + b. It panics on currency mismatch — combining amounts in
// different currencies without an explicit FX step is always a caller bug.
func (a Amount) Add(b Amount) Amount {
	a.mustMatch(b)
	return Amount{MinorUnits: a.MinorUnits + b.MinorUnits, Currency: a.Currency}
}

// Sub returns a - b. See Add for the currency-mismatch panic rationale.
func (a Amount) Sub(b Amount) Amount {
	a.mustMatch(b)
	return Amount{MinorUnits: a.MinorUnits - b.MinorUnits, Currency: a.Currency}
}

func (a Amount) mustMatch(b Amount) {
	if a.Currency != b.Currency {
		panic(fmt.Sprintf("money: currency mismatch: %s vs %s", a.Currency, b.Currency))
	}
}

// GreaterThan reports whether a > b (same currency required).
func (a Amount) GreaterThan(b Amount) bool {
	a.mustMatch(b)
	return a.MinorUnits > b.MinorUnits
}

// WithinTolerance reports whether a and b differ by no more than tolerance
// minor units. Used to decide whether a refreshed merchant quote has drifted
// enough from an approved amount to require reapproval.
func (a Amount) WithinTolerance(b Amount, tolerance Amount) bool {
	a.mustMatch(b)
	a.mustMatch(tolerance)
	diff := a.MinorUnits - b.MinorUnits
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance.MinorUnits
}

// String renders a human-readable "123.45 INR" form for logs/UI. It is never
// used for arithmetic or persistence — MinorUnits/Currency are authoritative.
func (a Amount) String() string {
	sign := ""
	v := a.MinorUnits
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d %s", sign, v/100, v%100, a.Currency)
}
