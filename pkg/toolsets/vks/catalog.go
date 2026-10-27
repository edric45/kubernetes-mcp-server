package vks

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
)

func initCatalog() []api.ServerTool {
	return []api.ServerTool{{
		Tool: api.Tool{
			Name: "vks_catalog",
			Description: "List what the vSphere Supervisor offers for new VKS clusters: Kubernetes releases (kubernetes_releases), " +
				"ClusterClasses (cluster_classes) or add-on releases (addon_releases). Use it to pick a version or ClusterClass, " +
				"or to see which add-ons and versions are available.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"list": {
						Type:        "string",
						Enum:        []any{"kubernetes_releases", "cluster_classes", "addon_releases"},
						Description: "What to list",
					},
					"addon": {
						Type:        "string",
						Description: "Only for addon_releases: one add-on name, e.g. istio, cert-manager (Optional)",
					},
					"include_not_ready": {
						Type:        "boolean",
						Description: "Only for kubernetes_releases: also list releases that are not ready (Optional, default false)",
					},
				},
				Required: []string{"list"},
			},
			Annotations: api.ToolAnnotations{
				Title:           "VKS: Catalog",
				ReadOnlyHint:    ptr.To(true),
				DestructiveHint: ptr.To(false),
				IdempotentHint:  ptr.To(true),
				OpenWorldHint:   ptr.To(true),
			},
		},
		RBAC: api.RBACBounded(
			catalogRBAC(kubernetesReleaseKind, "list"),
			catalogRBAC(clusterClassKind, "list"),
			catalogRBAC(addonReleaseKind, "list"),
		),
		Handler: catalog,
	}}
}

func catalog(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	p := api.WrapParams(params)
	what := p.RequiredString("list")
	addon := p.OptionalString("addon", "")
	includeNotReady := p.OptionalBool("include_not_ready", false)
	if err := p.Err(); err != nil {
		return api.NewToolCallResult("", err), nil
	}
	b := api.NewBuilderWithBudget(api.ResultLimit(params.Context))
	switch what {
	case "kubernetes_releases":
		items, err := list(params.Context, params.KubernetesClient, kubernetesReleaseKind, "", "")
		if err != nil {
			return api.NewToolCallResult("", fmt.Errorf("failed to list Kubernetes releases: %w", err)), nil
		}
		type row struct{ version, ready, compatible string }
		var rows []row
		for _, kr := range items {
			ready := condition(kr.Object, "Ready")
			if ready != "True" && !includeNotReady {
				continue
			}
			version, _, _ := unstructured.NestedString(kr.Object, "spec", "version")
			if version == "" {
				version = kr.GetName()
			}
			rows = append(rows, row{version, ready, condition(kr.Object, "Compatible")})
		}
		sort.Slice(rows, func(i, j int) bool { return versionLess(rows[j].version, rows[i].version) })
		b.WriteString("version | ready | compatible  (newest first)\n")
		for _, r := range rows {
			if b.WriteString(fmt.Sprintf("%s | %s | %s\n", r.version, r.ready, r.compatible)) {
				break
			}
		}
	case "cluster_classes":
		items, err := list(params.Context, params.KubernetesClient, clusterClassKind, CatalogNamespace, "")
		if err != nil {
			return api.NewToolCallResult("", fmt.Errorf("failed to list ClusterClasses: %w", err)), nil
		}
		names := make([]string, 0, len(items))
		for _, cc := range items {
			names = append(names, cc.GetName())
		}
		sort.Slice(names, func(i, j int) bool { return versionLess(names[i], names[j]) })
		b.WriteString(fmt.Sprintf("ClusterClasses in %s (newest last):\n%s\n", CatalogNamespace, strings.Join(names, "\n")))
	case "addon_releases":
		items, err := list(params.Context, params.KubernetesClient, addonReleaseKind, CatalogNamespace, "")
		if err != nil {
			return api.NewToolCallResult("", fmt.Errorf("failed to list add-on releases: %w", err)), nil
		}
		type row struct{ addon, version, release string }
		var rows []row
		for _, r := range items {
			name, _, _ := unstructured.NestedString(r.Object, "spec", "addonRef", "name")
			if addon != "" && name != addon {
				continue
			}
			version, _, _ := unstructured.NestedString(r.Object, "spec", "version")
			rows = append(rows, row{name, version, r.GetName()})
		}
		if len(rows) == 0 {
			return api.NewToolCallResult("", fmt.Errorf("no add-on releases found for %q", addon)), nil
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].addon != rows[j].addon {
				return rows[i].addon < rows[j].addon
			}
			return versionLess(rows[i].version, rows[j].version)
		})
		b.WriteString("addon | version | release (use with vks_addon_settings)\n")
		for _, r := range rows {
			if b.WriteString(fmt.Sprintf("%s | %s | %s\n", r.addon, r.version, r.release)) {
				break
			}
		}
	default:
		return api.NewToolCallResult("", fmt.Errorf("unknown list %q: use kubernetes_releases, cluster_classes or addon_releases", what)), nil
	}
	return api.NewToolCallResult(b.String(), nil), nil
}
