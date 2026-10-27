package vks

import (
	"sort"
	"strings"
	"testing"
)

// A trimmed copy of the builtin-generic-v3.7.0 osConfiguration/kubernetes schema shape.
func testSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"securityContext": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"appArmor": map[string]any{
						"type":        "object",
						"description": "AppArmor configures the appArmor profiles of the node.",
						"properties": map[string]any{
							"profiles": map[string]any{
								"type": "array",
								"items": map[string]any{
									"properties": map[string]any{
										"name": map[string]any{"type": "string", "description": "AppArmorProfile object name."},
									},
								},
							},
						},
					},
				},
			},
			"immutability": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"usrPartitionMode": map[string]any{
						"type": "string", "enum": []any{"ReadOnly", "ReadWrite"},
						"description": "UsrPartitionMode configures the /usr partition.",
					},
				},
			},
			"security": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"minimumTLSProtocol":   map[string]any{"type": "string", "description": "Minimum TLS protocol version."},
					"allowedUnsafeSysctls": map[string]any{"type": "array", "description": "Unsafe sysctls allowlist."},
				},
			},
		},
	}
}

func TestSchemaAt(t *testing.T) {
	t.Run("walks properties and array items", func(t *testing.T) {
		node, err := schemaAt(testSchema(), "securityContext.appArmor.profiles.name")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if node["type"] != "string" {
			t.Errorf("expected the name field, got %v", node)
		}
	})
	t.Run("unknown part lists the available children", func(t *testing.T) {
		_, err := schemaAt(testSchema(), "immutability.rootMode")
		if err == nil || !strings.Contains(err.Error(), "usrPartitionMode") {
			t.Errorf("expected an error naming usrPartitionMode, got %v", err)
		}
	})
	t.Run("empty path is the root", func(t *testing.T) {
		node, err := schemaAt(testSchema(), "")
		if err != nil || node["type"] != "object" {
			t.Errorf("expected the root, got %v, %v", node, err)
		}
	})
}

func TestDescribe(t *testing.T) {
	node, _ := schemaAt(testSchema(), "immutability.usrPartitionMode")
	out := describe(node, "osConfiguration.immutability.usrPartitionMode")
	for _, want := range []string{"type=string", "allowed values: ReadOnly, ReadWrite", "/usr partition"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
}

func TestSearch(t *testing.T) {
	paths := func(words ...string) []string {
		var out []string
		for _, s := range search(testSchema(), "osConfiguration", words) {
			out = append(out, s.Path)
		}
		sort.Strings(out)
		return out
	}
	t.Run("long word matches inside camelCase", func(t *testing.T) {
		got := paths("apparmor")
		if len(got) == 0 || got[0] != "osConfiguration.securityContext.appArmor" {
			t.Errorf("expected appArmor first, got %v", got)
		}
	})
	t.Run("short word matches whole words only", func(t *testing.T) {
		got := paths("tls")
		if len(got) != 1 || got[0] != "osConfiguration.security.minimumTLSProtocol" {
			t.Errorf("expected only minimumTLSProtocol (not allowedUnsafeSysctls), got %v", got)
		}
	})
	t.Run("all words must match", func(t *testing.T) {
		if got := paths("usr", "partition"); len(got) != 1 {
			t.Errorf("expected one match, got %v", got)
		}
		if got := paths("usr", "kerberos"); len(got) != 0 {
			t.Errorf("expected no match, got %v", got)
		}
	})
}

func TestVersionLess(t *testing.T) {
	versions := []string{"v1.35.6+vmware.2-vkr.3", "v1.36.4+vmware.2-vkr.3", "v1.9.0+vmware.1", "v1.36.2+vmware.2-vkr.3"}
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[i], versions[j]) })
	want := []string{"v1.9.0+vmware.1", "v1.35.6+vmware.2-vkr.3", "v1.36.2+vmware.2-vkr.3", "v1.36.4+vmware.2-vkr.3"}
	if strings.Join(versions, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", versions, want)
	}
}

func TestGib(t *testing.T) {
	cases := map[string]string{
		"2000Gi":         "2000 GiB",
		"1028459Mi":      "1004 GiB",
		"30281938556":    "28 GiB",
		unlimitedQuota:   "no limit",
		"not-a-quantity": "not-a-quantity",
	}
	for in, want := range cases {
		if got := gib(in); got != want {
			t.Errorf("gib(%q) = %q, want %q", in, got, want)
		}
	}
}
