package config

import (
	"os"
	"strings"
)

// WebToolsConfig is the web-tool keys the operator keeps in the server's
// environment, and who may use them.
//
// A person normally saves a key of their own (Account → Web tools) and the
// vendor bills them. A key here is the operator's own account with the vendor,
// so every call made with it is billed to the operator: it is offered to the
// accounts named in ALGEBRA_OPERATOR_EMAILS and to nobody else. With no address
// listed, no account can use it.
type WebToolsConfig struct {
	// Keys are by vendor id, read from the variables in WebToolKeyEnv. Only
	// vendors with a value are here.
	Keys map[string]string
	// OperatorEmails (ALGEBRA_OPERATOR_EMAILS, comma separated) are the
	// addresses of the operator's own accounts.
	OperatorEmails []string
}

// WebToolKeyEnv is the variable each vendor's operator key is read from.
var WebToolKeyEnv = []struct{ Vendor, Env string }{
	{"tinyfish", "TINY_FISH_API"},
	{"exa", "EXA_API"},
	{"firecrawl", "FIRECRAWL_API"},
	{"tavily", "TAVILY_API"},
}

// loadWebTools reads the keys and the operator's addresses. It checks nothing:
// the keys are checked for shape, and the addresses for being addresses, where
// they are put to use (internal/platform/wiring), so a mistake there names the
// variable without repeating its value.
func loadWebTools(c *WebToolsConfig) {
	for _, k := range WebToolKeyEnv {
		if v := strings.TrimSpace(os.Getenv(k.Env)); v != "" {
			if c.Keys == nil {
				c.Keys = map[string]string{}
			}
			c.Keys[k.Vendor] = v
		}
	}
	c.OperatorEmails = splitCSV(os.Getenv("ALGEBRA_OPERATOR_EMAILS"))
}
