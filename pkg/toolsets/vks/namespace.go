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

func initNamespace() []api.ServerTool {
	return []api.ServerTool{{
		Tool: api.Tool{
			Name: "vks_namespace_overview",
			Description: "Summarize a vSphere Namespace: its VKS clusters (version, phase, replicas, available updates), storage quota " +
				"and usage, and the VM classes it may use. Check it before creating or scaling clusters: a rollout needs room for " +
				"new VMs before old ones are removed.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"namespace": {Type: "string", Description: "vSphere Namespace (Optional, the configured namespace if not provided)"},
				},
			},
			Annotations: api.ToolAnnotations{
				Title:           "VKS: Namespace overview",
				ReadOnlyHint:    ptr.To(true),
				DestructiveHint: ptr.To(false),
				IdempotentHint:  ptr.To(true),
				OpenWorldHint:   ptr.To(true),
			},
		},
		RBAC: api.RBACBounded(
			namespaceRBAC(clusterKind),
			namespaceRBAC(storagePolicyQuotaKind),
			namespaceRBAC(storagePolicyUsageKind),
			namespaceRBAC(virtualMachineClassKind),
		),
		Handler: namespaceOverview,
	}}
}

func namespaceOverview(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	p := api.WrapParams(params)
	namespace := params.NamespaceOrDefault(p.OptionalString("namespace", ""))
	if err := p.Err(); err != nil {
		return api.NewToolCallResult("", err), nil
	}
	b := api.NewBuilderWithBudget(api.ResultLimit(params.Context))
	_ = b.Sections(
		func(b *api.BuilderWithBudget) error {
			b.WriteString(fmt.Sprintf("## Clusters in %s\n", namespace))
			items, err := list(params.Context, params.KubernetesClient, clusterKind, namespace, "")
			if err != nil {
				b.WriteString(fmt.Sprintf("*Cannot list clusters: %v*\n", err))
				return nil
			}
			if len(items) == 0 {
				b.WriteString("none\n")
			}
			for _, c := range items {
				b.WriteString(clusterSummary(c) + "\n")
			}
			return nil
		},
		func(b *api.BuilderWithBudget) error {
			b.WriteString("\n## Storage quota per storage policy\n")
			quotas, err := list(params.Context, params.KubernetesClient, storagePolicyQuotaKind, namespace, "")
			if err != nil {
				b.WriteString(fmt.Sprintf("*Cannot read quotas: %v*\n", err))
				return nil
			}
			for _, q := range quotas {
				limit, _, _ := unstructured.NestedString(q.Object, "status", "appliedLimit")
				if limit == "" {
					limit, _, _ = unstructured.NestedString(q.Object, "spec", "limit")
				}
				b.WriteString(fmt.Sprintf("- %s: limit=%s\n", q.GetName(), gib(limit)))
			}
			b.WriteString("\n## Storage used (non-zero)\n")
			usages, err := list(params.Context, params.KubernetesClient, storagePolicyUsageKind, namespace, "")
			if err != nil {
				b.WriteString(fmt.Sprintf("*Cannot read usage: %v*\n", err))
				return nil
			}
			for _, u := range usages {
				used, _, _ := unstructured.NestedString(u.Object, "status", "quotaUsage", "used")
				if used != "" && used != "0" {
					b.WriteString(fmt.Sprintf("- %s: %s\n", u.GetName(), gib(used)))
				}
			}
			return nil
		},
		func(b *api.BuilderWithBudget) error {
			b.WriteString("\n## VM classes\n")
			classes, err := list(params.Context, params.KubernetesClient, virtualMachineClassKind, namespace, "")
			if err != nil {
				b.WriteString(fmt.Sprintf("*Cannot list VM classes: %v*\n", err))
				return nil
			}
			sort.Slice(classes, func(i, j int) bool { return classes[i].GetName() < classes[j].GetName() })
			for _, v := range classes {
				cpus, _, _ := unstructured.NestedFieldNoCopy(v.Object, "spec", "hardware", "cpus")
				memory, _, _ := unstructured.NestedString(v.Object, "spec", "hardware", "memory")
				b.WriteString(fmt.Sprintf("- %s: %v vCPU, %s\n", v.GetName(), cpus, memory))
			}
			return nil
		},
	)
	return api.NewToolCallResult(b.String(), nil), nil
}

// clusterSummary is one line per cluster with the fields that matter for planning.
func clusterSummary(c unstructured.Unstructured) string {
	class, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "classRef", "name")
	if class == "" {
		class, _, _ = unstructured.NestedString(c.Object, "spec", "topology", "class")
	}
	version, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "version")
	phase, _, _ := unstructured.NestedString(c.Object, "status", "phase")
	line := fmt.Sprintf("- %s: class=%s version=%s phase=%s available=%s rollingOut=%s controlPlane=%s workers=%s",
		c.GetName(), class, version, phase, condition(c.Object, "Available"), condition(c.Object, "RollingOut"),
		replicas(c.Object, "controlPlane"), replicas(c.Object, "workers"))
	// UpdatesAvailable is still reported only in the v1beta1 compatibility conditions.
	old, _, _ := unstructured.NestedSlice(c.Object, "status", "deprecated", "v1beta1", "conditions")
	for _, x := range old {
		m, _ := x.(map[string]any)
		if m["type"] == "UpdatesAvailable" && m["status"] == "True" {
			line += " updatesAvailable=" + strings.TrimSpace(fmt.Sprint(m["message"]))
		}
	}
	return line
}

func replicas(obj map[string]any, field string) string {
	ready, _, _ := unstructured.NestedInt64(obj, "status", field, "readyReplicas")
	desired, _, _ := unstructured.NestedInt64(obj, "status", field, "desiredReplicas")
	return fmt.Sprintf("%d/%d", ready, desired)
}
