package config

import (
	"reflect"
	"testing"
)

func TestLoadWebTools_ReadsTheKeysAndTheOperatorsAddresses(t *testing.T) {
	for _, k := range WebToolKeyEnv {
		t.Setenv(k.Env, "")
	}
	t.Setenv("ALGEBRA_OPERATOR_EMAILS", "")

	var none WebToolsConfig
	loadWebTools(&none)
	if len(none.Keys) != 0 || len(none.OperatorEmails) != 0 {
		t.Fatalf("nothing set, nothing read: %+v", none)
	}

	t.Setenv("TINY_FISH_API", "  sk-tinyfish-0123456789  ")
	t.Setenv("EXA_API", "exa-0123456789")
	t.Setenv("ALGEBRA_OPERATOR_EMAILS", " Me@Example.com , other@example.com,, ")
	var c WebToolsConfig
	loadWebTools(&c)
	want := map[string]string{"tinyfish": "sk-tinyfish-0123456789", "exa": "exa-0123456789"}
	if !reflect.DeepEqual(c.Keys, want) {
		t.Errorf("keys: %v, want %v", c.Keys, want)
	}
	if !reflect.DeepEqual(c.OperatorEmails, []string{"Me@Example.com", "other@example.com"}) {
		t.Errorf("operator addresses: %q", c.OperatorEmails)
	}
}

// The variable the operator uses for TinyFish is TINY_FISH_API.
func TestWebToolKeyEnv_TinyFishIsTinyFishAPI(t *testing.T) {
	for _, k := range WebToolKeyEnv {
		if k.Vendor == "tinyfish" && k.Env == "TINY_FISH_API" {
			return
		}
	}
	t.Fatal("TINY_FISH_API is not the TinyFish key")
}
