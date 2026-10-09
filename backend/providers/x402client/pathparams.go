package x402client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// A templated endpoint has placeholders where the caller's values go:
// "https://api.example.com/v1/{chain}/tokens/{mint}", or Google's
// "…/v3/projects/{projectsId}:translateText". The runner fills each from the
// input field of the same name. The field is then part of the path and no
// longer of the query or the body, as it would be for any REST API.

// templateRE matches a {name} placeholder in a URL path, raw or as a URL
// writes it (%7Bname%7D, which is how a normalised endpoint carries it).
var templateRE = regexp.MustCompile(`(?i)(?:\{|%7B)([A-Za-z0-9_.-]{1,64})(?:\}|%7D)`)

// maxPathValue bounds one value placed in a path.
const maxPathValue = 256

// expandPath fills a templated endpoint from the input (canonical JSON) and
// returns the concrete URL together with the input that is left over. An
// endpoint with no placeholders is returned as it was.
//
// A value is escaped as one path segment, so it can't add segments, a query or
// a fragment: values with a slash, a backslash, "?" or "#", a control
// character, or that are "." or "..", are refused outright rather than
// escaped, because a server that decodes %2F back into a slash would otherwise
// be steered to a different resource than the one that was priced. Placeholders
// the input doesn't fill are an error, never sent as literal braces.
func expandPath(endpoint string, canon []byte) (string, []byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", nil, errors.New("the endpoint isn't a valid URL")
	}
	escaped := u.EscapedPath()
	if !templateRE.MatchString(escaped) {
		if strings.ContainsAny(escaped, "{}") || strings.Contains(strings.ToLower(escaped), "%7b") {
			return "", nil, errors.New("the endpoint's path has a placeholder Algebra can't read")
		}
		return endpoint, canon, nil
	}

	names := templateRE.FindAllStringSubmatch(escaped, -1)
	need := make([]string, 0, len(names))
	for _, m := range names {
		if !containsString(need, m[1]) {
			need = append(need, m[1])
		}
	}
	var fields map[string]any
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return "", nil, fmt.Errorf("this endpoint takes path parameters (%s), so the input must be a JSON object that names them", strings.Join(need, ", "))
	}

	var firstErr error
	filled := templateRE.ReplaceAllStringFunc(escaped, func(m string) string {
		name := templateRE.FindStringSubmatch(m)[1]
		raw, ok := fields[name]
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("this endpoint needs the path parameter %q: put it in the input", name)
			}
			return m
		}
		v, err := pathValue(name, raw)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return url.PathEscape(v)
	})
	if firstErr != nil {
		return "", nil, firstErr
	}
	// Anything still in braces is a placeholder this didn't understand.
	if strings.ContainsAny(filled, "{}") || strings.Contains(strings.ToLower(filled), "%7b") {
		return "", nil, errors.New("the endpoint's path has a placeholder Algebra can't fill in")
	}
	p, err := url.PathUnescape(filled)
	if err != nil {
		return "", nil, errors.New("a path parameter can't be placed in the URL")
	}
	u.Path, u.RawPath = p, filled

	for _, name := range need {
		delete(fields, name)
	}
	rest, err := json.Marshal(fields)
	if err != nil {
		return "", nil, err
	}
	if rest, err = econ.Canonicalize(rest); err != nil {
		return "", nil, err
	}
	return u.String(), rest, nil
}

// pathValue is one input field as the text that goes in the path.
func pathValue(name string, v any) (string, error) {
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case json.Number:
		s = t.String()
	case bool:
		s = fmt.Sprint(t)
	default:
		return "", fmt.Errorf("the path parameter %q must be a string, a number or a boolean", name)
	}
	switch {
	case strings.TrimSpace(s) == "":
		return "", fmt.Errorf("the path parameter %q is empty", name)
	case len(s) > maxPathValue:
		return "", fmt.Errorf("the path parameter %q is longer than %d characters", name, maxPathValue)
	case s == "." || s == "..":
		return "", fmt.Errorf("the path parameter %q isn't a usable value", name)
	case strings.ContainsAny(s, "/\\?#"):
		return "", fmt.Errorf("the path parameter %q can't contain / \\ ? or #: it is one segment of the path", name)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("the path parameter %q has a control character", name)
		}
	}
	return s, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
