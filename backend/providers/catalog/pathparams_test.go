package catalog

import (
	"slices"
	"testing"
)

func TestPathParams(t *testing.T) {
	for path, want := range map[string][]string{
		"v1/price":                                         nil,
		"v1/{chain}/tokens/{mint}":                         {"chain", "mint"},
		"v3/projects/{projectsId}:translateText":           {"projectsId"},
		"v3/projects/{projectsId}/locations/{locationsId}": {"projectsId", "locationsId"},
		"a/{id}/b/{id}":                                    {"id"},
		"a/%7Bid%7D/b/%7bname%7d":                          {"id", "name"},
		"a/{}/b/{with space}/c/{ok}":                       {"ok"},
	} {
		if got := PathParams(path); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", path, got, want)
		}
	}
}

func TestBraceTemplate(t *testing.T) {
	for in, want := range map[string]string{
		"/api/token/:mint/price": "/api/token/{mint}/price",
		"/:a/:b":                 "/{a}/{b}",
		":first/x":               "{first}/x",
		"/v1/{already}/x":        "/v1/{already}/x",
		"/api/time:now":          "/api/time:now", // a colon inside a segment is not a parameter
		"/plain":                 "/plain",
		"/api/:bad-name/x":       "/api/:bad-name/x",
	} {
		if got := BraceTemplate(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestUnescapeBraces(t *testing.T) {
	if got := UnescapeBraces("v1/%7Bmint%7D/%7bx%7d"); got != "v1/{mint}/{x}" {
		t.Errorf("got %q", got)
	}
}
