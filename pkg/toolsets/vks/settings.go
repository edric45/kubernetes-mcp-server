package vks

import (
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
)

func initSettings() []api.ServerTool {
	readOnly := api.ToolAnnotations{
		ReadOnlyHint:    ptr.To(true),
		DestructiveHint: ptr.To(false),
		IdempotentHint:  ptr.To(true),
		OpenWorldHint:   ptr.To(true),
	}
	clusterClass, addon := readOnly, readOnly
	clusterClass.Title = "VKS: ClusterClass settings"
	addon.Title = "VKS: Add-on settings"
	return []api.ServerTool{
		{
			Tool: api.Tool{
				Name: "vks_cluster_class_settings",
				Description: "Explain what a VKS ClusterClass lets you configure in a cluster spec (its variables): description, type, " +
					"allowed values and sub-settings. Give 'search' to answer 'does VKS expose X?' (e.g. apparmor, tls, proxy, ntp); " +
					"give 'path' to explain one setting (e.g. osConfiguration.immutability); give neither to list the top-level variables.",
				InputSchema: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"cluster_class": {Type: "string", Description: "ClusterClass name, e.g. builtin-generic-v3.7.0 (see vks_catalog)"},
						"search":        {Type: "string", Description: "Words to find in setting names and descriptions (Optional)"},
						"path":          {Type: "string", Description: "Dotted path of one setting, e.g. kubernetes.security (Optional)"},
					},
					Required: []string{"cluster_class"},
				},
				Annotations: clusterClass,
			},
			RBAC:    api.RBACBounded(catalogRBAC(clusterClassKind, "get")),
			Handler: clusterClassSettings,
		},
		{
			Tool: api.Tool{
				Name: "vks_addon_settings",
				Description: "Explain the settings a VKS add-on release accepts in its AddonConfig: description, type, allowed values, " +
					"defaults and sub-settings. Use before writing an AddonConfig.",
				InputSchema: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"release": {Type: "string", Description: "Add-on release name from vks_catalog addon_releases"},
						"path":    {Type: "string", Description: "Dotted path into the settings, e.g. istio.meshConfig (Optional)"},
					},
					Required: []string{"release"},
				},
				Annotations: addon,
			},
			RBAC: api.RBACBounded(
				catalogRBAC(addonReleaseKind, "get"),
				catalogRBAC(addonConfigDefinitionKind, "get"),
			),
			Handler: addonSettings,
		},
	}
}

// clusterClassVariables returns each variable name with its OpenAPI schema.
func clusterClassVariables(params api.ToolHandlerParams, name string) (map[string]map[string]any, error) {
	cc, err := get(params.Context, params.KubernetesClient, clusterClassKind, CatalogNamespace, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get ClusterClass %q: %w", name, err)
	}
	vars, _, _ := unstructured.NestedSlice(cc.Object, "status", "variables")
	out := map[string]map[string]any{}
	for _, v := range vars {
		m, _ := v.(map[string]any)
		defs, _ := m["definitions"].([]any)
		if len(defs) == 0 {
			continue
		}
		def, _ := defs[0].(map[string]any)
		schema, _, _ := unstructured.NestedMap(def, "schema", "openAPIV3Schema")
		if n, ok := m["name"].(string); ok && schema != nil {
			out[n] = schema
		}
	}
	return out, nil
}

func clusterClassSettings(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	p := api.WrapParams(params)
	name := p.RequiredString("cluster_class")
	query := strings.TrimSpace(p.OptionalString("search", ""))
	path := strings.TrimSpace(p.OptionalString("path", ""))
	if err := p.Err(); err != nil {
		return api.NewToolCallResult("", err), nil
	}
	vars, err := clusterClassVariables(params, name)
	if err != nil {
		return api.NewToolCallResult("", err), nil
	}
	b := api.NewBuilderWithBudget(api.ResultLimit(params.Context))
	switch {
	case query != "":
		var hits []setting
		for _, top := range sortedKeys(toAny(vars)) {
			hits = append(hits, search(vars[top], top, strings.Fields(query))...)
		}
		if len(hits) == 0 {
			return api.NewToolCallResult(fmt.Sprintf("No setting in %s matches %q.", name, query), nil), nil
		}
		b.WriteString(fmt.Sprintf("%d setting(s) in %s match %q:\n", len(hits), name, query))
		for _, h := range hits {
			if b.WriteString(fmt.Sprintf("- %s: %s\n", h.Path, shorten(h.Description, 300))) {
				break
			}
		}
	case path != "":
		top, rest, _ := strings.Cut(path, ".")
		schema, ok := vars[top]
		if !ok {
			return api.NewToolCallResult("", fmt.Errorf("no variable %q in %s; variables: %s",
				top, name, strings.Join(sortedKeys(toAny(vars)), ", "))), nil
		}
		node, err := schemaAt(schema, rest)
		if err != nil {
			return api.NewToolCallResult("", err), nil
		}
		b.WriteString(describe(node, path))
	default:
		b.WriteString(fmt.Sprintf("Top-level variables of %s:\n", name))
		for _, top := range sortedKeys(toAny(vars)) {
			if b.WriteString(fmt.Sprintf("- %s: %s\n", top, shorten(oneLine(vars[top]["description"]), 200))) {
				break
			}
		}
	}
	return api.NewToolCallResult(b.String(), nil), nil
}

func addonSettings(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	p := api.WrapParams(params)
	release := p.RequiredString("release")
	path := strings.TrimSpace(p.OptionalString("path", ""))
	if err := p.Err(); err != nil {
		return api.NewToolCallResult("", err), nil
	}
	rel, err := get(params.Context, params.KubernetesClient, addonReleaseKind, CatalogNamespace, release)
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to get add-on release %q (names come from vks_catalog addon_releases): %w", release, err)), nil
	}
	defName, _, _ := unstructured.NestedString(rel.Object, "spec", "addonConfigDefinitionRef", "name")
	defNamespace, _, _ := unstructured.NestedString(rel.Object, "spec", "addonConfigDefinitionRef", "namespace")
	if defNamespace == "" {
		defNamespace = CatalogNamespace
	}
	def, err := get(params.Context, params.KubernetesClient, addonConfigDefinitionKind, defNamespace, defName)
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to get settings definition %q: %w", defName, err)), nil
	}
	schema, _, _ := unstructured.NestedMap(def.Object, "spec", "schema")
	if inner, ok := schema["openAPIV3Schema"].(map[string]any); ok {
		schema = inner
	}
	node, err := schemaAt(schema, path)
	if err != nil {
		return api.NewToolCallResult("", err), nil
	}
	return api.NewToolCallResult(describe(node, path), nil), nil
}

func toAny(m map[string]map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
