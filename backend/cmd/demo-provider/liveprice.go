package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// jupiterPriceURL is Jupiter's free price API (no key). It prices mainnet mints.
const jupiterPriceURL = "https://lite-api.jup.ag/price/v3"

// devnetToMainnet maps devnet mints to the mainnet token they stand for, so a
// devnet USDC address is priced as USDC.
var devnetToMainnet = map[string]string{
	"4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
}

type livePrice struct {
	Mint string
	USD  string
	At   time.Time
}

var priceClient = &http.Client{Timeout: 8 * time.Second}

// fetchLivePrice asks Jupiter for the current USD price of the request's mint.
// It never makes a number up: no mint, an unknown mint or a failed lookup is an error.
func fetchLivePrice(ctx context.Context, in map[string]any) (*livePrice, error) {
	mint, _ := in["mint"].(string)
	if mint == "" {
		return nil, errors.New(`the request has no "mint"`)
	}
	lookup := mint
	if m, ok := devnetToMainnet[mint]; ok {
		lookup = m
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jupiterPriceURL+"?ids="+url.QueryEscape(lookup), nil)
	if err != nil {
		return nil, err
	}
	res, err := priceClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jupiter: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jupiter answered %d", res.StatusCode)
	}
	var body map[string]struct {
		USDPrice float64 `json:"usdPrice"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("jupiter: %w", err)
	}
	q, ok := body[lookup]
	if !ok || q.USDPrice <= 0 {
		return nil, fmt.Errorf("jupiter has no price for %s", lookup)
	}
	return &livePrice{Mint: mint, USD: strconv.FormatFloat(q.USDPrice, 'f', -1, 64), At: time.Now()}, nil
}

// hasLive says whether a class of work has a live source. The other classes
// answer with the stable made-up data in personas.go.
func hasLive(class string) bool {
	return class == "token.price" || class == "weather.forecast"
}

// fetchLive is the live answer for one class of work, fetched before payment.
func fetchLive(ctx context.Context, class string, in map[string]any) (map[string]any, error) {
	switch class {
	case "token.price":
		q, err := fetchLivePrice(ctx, in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"mint": q.Mint, "price_usd": q.USD, "source": "Jupiter Price API (live, mainnet)", "as_of": q.At.UTC().Format(time.RFC3339)}, nil
	case "weather.forecast":
		return fetchWeather(ctx, in)
	}
	return nil, fmt.Errorf("no live source for %s", class)
}
