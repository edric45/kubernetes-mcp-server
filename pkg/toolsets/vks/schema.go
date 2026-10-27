package vks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Pure helpers for the OpenAPI schemas VKS publishes for ClusterClass
// variables and add-on settings, and for formatting values. No API calls.

// children returns the properties of an object schema, or of an array's items.
func children(node map[string]any) map[string]any {
	if p, ok := node["properties"].(map[string]any); ok {
		return p
	}
	if items, ok := node["items"].(map[string]any); ok {
		if p, ok := items["properties"].(map[string]any); ok {
			return p
		}
	}
	return nil
}

func oneLine(s any) string {
	str, _ := s.(string)
	return strings.Join(strings.Fields(str), " ")
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// schemaAt walks a dotted path ("a.b.c") through a schema's properties.
func schemaAt(root map[string]any, path string) (map[string]any, error) {
	node := root
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		props := children(node)
		next, ok := props[part].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("no setting %q here; available: %s", part, strings.Join(sortedKeys(props), ", "))
		}
		node = next
	}
	return node, nil
}

// describe renders one schema node: description, type, allowed values, default, and its children.
func describe(node map[string]any, path string) string {
	var b strings.Builder
	if path == "" {
		path = "(top level)"
	}
	fmt.Fprintf(&b, "%s  type=%v\n", path, valueOr(node["type"], "?"))
	if d := oneLine(node["description"]); d != "" {
		fmt.Fprintf(&b, "  %s\n", d)
	}
	if enum, ok := node["enum"].([]any); ok && len(enum) > 0 {
		vals := make([]string, 0, len(enum))
		for _, v := range enum {
			vals = append(vals, fmt.Sprint(v))
		}
		fmt.Fprintf(&b, "  allowed values: %s\n", strings.Join(vals, ", "))
	}
	if def, ok := node["default"]; ok {
		raw, _ := json.Marshal(def)
		fmt.Fprintf(&b, "  default: %s\n", raw)
	}
	props := children(node)
	for _, name := range sortedKeys(props) {
		child, _ := props[name].(map[string]any)
		fmt.Fprintf(&b, "  - %s (%v): %s\n", name, valueOr(child["type"], "?"), shorten(oneLine(child["description"]), 300))
	}
	return b.String()
}

// setting is one schema path found by search.
type setting struct{ Path, Description string }

// search returns every path whose name or description contains all words
// (case-insensitive). Short words (under 5 letters) must match a whole word,
// so "tls" finds minimumTLSProtocol but not allowedUnsafeSysctls; longer words
// may also match inside a word, so "apparmor" finds appArmor.
func search(root map[string]any, prefix string, words []string) []setting {
	var all []setting
	all = append(all, setting{prefix, oneLine(root["description"])})
	walk(root, prefix, &all)
	var hits []setting
	for _, s := range all {
		text := s.Path + " " + s.Description
		if containsAll(tokenize(text), strings.ToLower(text), words) {
			hits = append(hits, s)
		}
	}
	return hits
}

func walk(node map[string]any, path string, out *[]setting) {
	props := children(node)
	for _, name := range sortedKeys(props) {
		child, _ := props[name].(map[string]any)
		p := name
		if path != "" {
			p = path + "." + name
		}
		*out = append(*out, setting{p, oneLine(child["description"])})
		walk(child, p, out)
	}
}

var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])|([A-Z]+)([A-Z][a-z])`)
var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// tokenize splits camelCase, dots, dashes and spaces into lower-case words:
// "minimumTLSProtocol" -> minimum, tls, protocol.
func tokenize(s string) map[string]bool {
	s = camelBoundary.ReplaceAllString(s, "$1$3 $2$4")
	tokens := map[string]bool{}
	for _, t := range nonWord.Split(strings.ToLower(s), -1) {
		if t != "" {
			tokens[t] = true
		}
	}
	return tokens
}

func containsAll(tokens map[string]bool, lower string, words []string) bool {
	for _, w := range words {
		w = strings.ToLower(w)
		if !tokens[w] && (len(w) < 5 || !strings.Contains(lower, w)) {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func valueOr(v any, def string) string {
	if v == nil {
		return def
	}
	return fmt.Sprint(v)
}

var digits = regexp.MustCompile(`\d+`)

// versionLess orders VKS release versions numerically:
// v1.35.6+vmware.2-vkr.3 < v1.36.2+vmware.2-vkr.3.
func versionLess(a, b string) bool {
	na, nb := digits.FindAllString(a, -1), digits.FindAllString(b, -1)
	for i := 0; i < len(na) && i < len(nb); i++ {
		x, _ := strconv.Atoi(na[i])
		y, _ := strconv.Atoi(nb[i])
		if x != y {
			return x < y
		}
	}
	return len(na) < len(nb)
}

var quantity = regexp.MustCompile(`^(\d+(?:\.\d+)?)(Ki|Mi|Gi|Ti)?$`)

// gib renders a Kubernetes quantity (or plain bytes) as GiB, e.g. "1028459Mi" -> "1004 GiB".
func gib(q string) string {
	if q == unlimitedQuota {
		return "no limit"
	}
	m := quantity.FindStringSubmatch(q)
	if m == nil {
		return q
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	mult := map[string]float64{"": 1, "Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40}[m[2]]
	return fmt.Sprintf("%.0f GiB", n*mult/(1<<30))
}
