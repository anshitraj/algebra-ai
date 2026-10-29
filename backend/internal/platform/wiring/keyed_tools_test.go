package wiring

import (
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/providers/keyed"
)

// Every vendor the keyed tools know has a variable its operator key is read
// from, and the other way round.
func TestWebToolKeyEnv_CoversEveryVendor(t *testing.T) {
	vendors := keyed.NewTools(nil).Vendors()
	env := map[string]string{}
	for _, k := range config.WebToolKeyEnv {
		env[k.Vendor] = k.Env
	}
	if len(env) != len(vendors) {
		t.Errorf("%d variables for %d vendors", len(env), len(vendors))
	}
	for _, v := range vendors {
		if env[v.ID] == "" {
			t.Errorf("no environment variable for the %s key", v.ID)
		}
	}
}

func testToolKeysService() *app.ToolKeys {
	tools := keyed.NewTools(nil)
	var vendors []app.ToolVendor
	for _, v := range tools.Vendors() {
		vendors = append(vendors, app.ToolVendor{ID: v.ID, Name: v.Name})
	}
	return app.NewToolKeys(nil, nil, vendors, tools.Candidates, nil)
}

func TestUseOperatorKeys(t *testing.T) {
	const secret = "sk-tinyfish-0123456789abcdef"

	if err := useOperatorKeys(testToolKeysService(), config.WebToolsConfig{}, nil); err != nil {
		t.Errorf("no keys, nothing to do: %v", err)
	}
	ok := config.WebToolsConfig{Keys: map[string]string{"tinyfish": secret}, OperatorEmails: []string{"Me@Example.com"}}
	if err := useOperatorKeys(testToolKeysService(), ok, nil); err != nil {
		t.Errorf("a key and an address: %v", err)
	}
	// Keys with nobody named are accepted (and logged), and give nobody the key.
	if err := useOperatorKeys(testToolKeysService(), config.WebToolsConfig{Keys: ok.Keys}, nil); err != nil {
		t.Errorf("a key and no address: %v", err)
	}

	bad := config.WebToolsConfig{Keys: ok.Keys, OperatorEmails: []string{"not an address"}}
	if err := useOperatorKeys(testToolKeysService(), bad, nil); err == nil || !strings.Contains(err.Error(), "ALGEBRA_OPERATOR_EMAILS") {
		t.Errorf("an entry that is not an address stops the start, naming the variable: %v", err)
	}
	for name, keys := range map[string]map[string]string{
		"short key":      {"tinyfish": "short"},
		"key with space": {"tinyfish": "sk tinyfish 0123456789"},
		"unknown vendor": {"nope": secret},
	} {
		err := useOperatorKeys(testToolKeysService(), config.WebToolsConfig{Keys: keys, OperatorEmails: ok.OperatorEmails}, nil)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the error repeats the key: %v", name, err)
		}
	}
}
