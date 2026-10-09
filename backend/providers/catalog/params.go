package catalog

import (
	"encoding/json"
	"slices"
	"strings"
)

// SchemaParams reads the parameter names an endpoint's published input
// description declares, and which of them it says are required. Directories
// publish this in several shapes, all handled:
//
//   - a JSON Schema object: {"type":"object","properties":{...},"required":[...]}
//   - the bazaar "http" shape, whose queryParams, body and inputSchema are
//     each a JSON Schema or an example object:
//     {"type":"http","method":"GET","queryParams":{"address":"..."}}
//
// known is false when there is nothing to read (no description, or one with
// no parameters named), which is different from an endpoint that takes none.
func SchemaParams(raw json.RawMessage) (params, required []string, known bool) {
	var top map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &top) != nil {
		return nil, nil, false
	}
	add := func(p, r []string) {
		for _, k := range p {
			if k = strings.TrimSpace(k); k != "" && !slices.Contains(params, k) {
				params = append(params, k)
			}
		}
		for _, k := range r {
			if k = strings.TrimSpace(k); k != "" && !slices.Contains(required, k) {
				required = append(required, k)
			}
		}
	}
	if _, ok := top["properties"]; ok {
		add(objectParams(raw))
		return params, required, len(params) > 0
	}
	for _, key := range []string{"queryParams", "query", "body", "inputSchema"} {
		if v, ok := top[key]; ok {
			add(objectParams(v))
		}
	}
	return params, required, len(params) > 0
}

// objectParams reads one object: a JSON Schema's properties, or an example's
// keys. Schema keywords are never taken for parameter names.
func objectParams(raw json.RawMessage) ([]string, []string) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil, nil
	}
	if props, ok := obj["properties"]; ok {
		var p map[string]json.RawMessage
		if json.Unmarshal(props, &p) != nil {
			return nil, nil
		}
		var req []string
		_ = json.Unmarshal(obj["required"], &req)
		names := make([]string, 0, len(p))
		for k := range p {
			names = append(names, k)
		}
		slices.Sort(names)
		return names, req
	}
	var names []string
	for k := range obj {
		switch k {
		case "type", "required", "additionalProperties", "description", "title", "$schema", "examples", "example":
			continue
		}
		names = append(names, k)
	}
	slices.Sort(names)
	return names, nil
}
