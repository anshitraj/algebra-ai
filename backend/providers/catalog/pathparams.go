package catalog

import (
	"regexp"
	"strings"
)

// A templated path has placeholders where the caller's values go:
// "v1/{chain}/tokens/{mint}", or Google's "v3/projects/{projectsId}:translateText".
// Catalogs write them as {name}; some APIs write them express-style, as a
// ":name" path segment. Algebra keeps one form, {name}, so that the runner has
// one thing to fill in.

// paramRE matches a {name} placeholder, raw or as a URL turns it into
// (%7Bname%7D).
var paramRE = regexp.MustCompile(`(?i)(?:\{|%7B)([A-Za-z0-9_.-]{1,64})(?:\}|%7D)`)

// expressRE is one express-style path segment: ":mint".
var expressRE = regexp.MustCompile(`^:([A-Za-z_][A-Za-z0-9_]{0,63})$`)

// PathParams lists a path's parameters, in order of first appearance and
// without repeats: "v1/{chain}/tokens/{mint}" gives [chain mint].
func PathParams(path string) []string {
	var out []string
	for _, m := range paramRE.FindAllStringSubmatch(path, -1) {
		if !contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// BraceTemplate rewrites express-style segments as {name} placeholders, and
// leaves everything else as it was: "/api/token/:mint/price" becomes
// "/api/token/{mint}/price".
func BraceTemplate(path string) string {
	if !strings.Contains(path, "/:") && !strings.HasPrefix(path, ":") {
		return path
	}
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if m := expressRE.FindStringSubmatch(s); m != nil {
			segs[i] = "{" + m[1] + "}"
		}
	}
	return strings.Join(segs, "/")
}

// UnescapeBraces turns %7B and %7D back into braces: a URL's string form
// escapes them, and a person reading the path doesn't want to see that.
func UnescapeBraces(s string) string {
	return strings.NewReplacer("%7B", "{", "%7b", "{", "%7D", "}", "%7d", "}").Replace(s)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
