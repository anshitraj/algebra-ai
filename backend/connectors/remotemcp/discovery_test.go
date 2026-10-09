package remotemcp

import (
	"reflect"
	"testing"
)

// Swiggy's challenge advertises the origin-root metadata URL, which 404s;
// the document actually lives under the endpoint path. Discovery must try
// the RFC 9728 location first, then that one, then the root.
func TestWellKnownResourceMetadataURLs_IncludesEndpointAppended(t *testing.T) {
	got := wellKnownResourceMetadataURLs("https://mcp.swiggy.com/im")
	want := []string{
		"https://mcp.swiggy.com/.well-known/oauth-protected-resource/im",
		"https://mcp.swiggy.com/im/.well-known/oauth-protected-resource",
		"https://mcp.swiggy.com/.well-known/oauth-protected-resource",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if got := wellKnownResourceMetadataURLs("https://mcp.example.com"); len(got) != 1 {
		t.Errorf("root endpoint should yield only the root location, got %v", got)
	}
}
