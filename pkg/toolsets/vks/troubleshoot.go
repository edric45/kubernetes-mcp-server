package vks

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
	"github.com/containers/kubernetes-mcp-server/pkg/config"
)

func initTroubleshoot() []api.ServerPrompt {
	return []api.ServerPrompt{{
		Prompt: config.Prompt{
			Name:  "vks-cluster-troubleshoot",
			Title: "VKS Cluster Troubleshoot",
			Description: "Gather the state of a VKS cluster (conditions, node pools, every machine and its failing conditions) " +
				"and the namespace's capacity, to find why a cluster is not available or a rollout is stuck.",
			Arguments: []config.PromptArgument{
				{Name: "namespace", Description: "vSphere Namespace of the cluster", Required: true},
				{Name: "cluster", Description: "Cluster name", Required: true},
			},
		},
		RBAC: api.RBACBounded(
			namespaceRBAC(clusterKind),
			namespaceRBAC(machineDeploymentKind),
			namespaceRBAC(machineKind),
			namespaceRBAC(storagePolicyQuotaKind),
			namespaceRBAC(storagePolicyUsageKind),
		),
		Handler: troubleshoot,
	}}
}

const troubleshootAssistantText = "I'll analyze the cluster, node pool and machine state to find what blocks the cluster."

func troubleshoot(params api.PromptHandlerParams) (*api.PromptCallResult, error) {
	args := params.GetArguments()
	namespace, name := args["namespace"], args["cluster"]
	if namespace == "" || name == "" {
		return nil, fmt.Errorf("namespace and cluster are required")
	}
	selector := "cluster.x-k8s.io/cluster-name=" + name
	b := api.NewPromptBudget(params.Context, troubleshootAssistantText)
	_ = b.Sections(
		api.Text(fmt.Sprintf("# VKS cluster %s (namespace %s)\n\n## Cluster\n", name, namespace)),
		func(b *api.BuilderWithBudget) error {
			c, err := get(params.Context, params.KubernetesClient, clusterKind, namespace, name)
			if err != nil {
				b.WriteString(fmt.Sprintf("*Cannot get cluster: %v*\n", err))
				return nil
			}
			b.WriteString(clusterSummary(*c) + "\n")
			b.WriteString(notTrue(c.Object, "  "))
			return nil
		},
		api.Text("\n## Node pools (MachineDeployments)\n"),
		func(b *api.BuilderWithBudget) error {
			mds, err := list(params.Context, params.KubernetesClient, machineDeploymentKind, namespace, selector)
			if err != nil {
				b.WriteString(fmt.Sprintf("*Cannot list node pools: %v*\n", err))
				return nil
			}
			for _, md := range mds {
				desired, _, _ := unstructured.NestedInt64(md.Object, "spec", "replicas")
				ready, _, _ := unstructured.NestedInt64(md.Object, "status", "readyReplicas")
				upToDate, _, _ := unstructured.NestedInt64(md.Object, "status", "upToDateReplicas")
				phase, _, _ := unstructured.NestedString(md.Object, "status", "phase")
				b.WriteString(fmt.Sprintf("- %s: desired=%d ready=%d upToDate=%d phase=%s\n", md.GetName(), desired, ready, upToDate, phase))
			}
			return nil
		},
		api.Text("\n## Machines (one VM each) and failing conditions\n"),
		func(b *api.BuilderWithBudget) error {
			machines, err := list(params.Context, params.KubernetesClient, machineKind, namespace, selector)
			if err != nil {
				b.WriteString(fmt.Sprintf("*Cannot list machines: %v*\n", err))
				return nil
			}
			for _, m := range machines {
				phase, _, _ := unstructured.NestedString(m.Object, "status", "phase")
				b.WriteString(fmt.Sprintf("- %s: phase=%s upToDate=%s age=%s\n",
					m.GetName(), phase, condition(m.Object, "UpToDate"), m.GetCreationTimestamp().UTC().Format("2006-01-02 15:04")))
				b.WriteString(notTrue(m.Object, "    "))
			}
			return nil
		},
		api.Text("\n## Namespace storage\n"),
		func(b *api.BuilderWithBudget) error {
			quotas, _ := list(params.Context, params.KubernetesClient, storagePolicyQuotaKind, namespace, "")
			for _, q := range quotas {
				limit, _, _ := unstructured.NestedString(q.Object, "status", "appliedLimit")
				b.WriteString(fmt.Sprintf("- quota %s: %s\n", q.GetName(), gib(limit)))
			}
			usages, _ := list(params.Context, params.KubernetesClient, storagePolicyUsageKind, namespace, "")
			for _, u := range usages {
				used, _, _ := unstructured.NestedString(u.Object, "status", "quotaUsage", "used")
				if used != "" && used != "0" {
					b.WriteString(fmt.Sprintf("- used %s: %s\n", u.GetName(), gib(used)))
				}
			}
			return nil
		},
		api.Text(`
## Analysis

Using the data above, determine:
1. Is the cluster Available? If not, which condition explains it?
2. Is a rollout in progress (machines with upToDate=False, node pools with upToDate < desired)? What triggered it?
3. For machines stuck in Provisioning: look for storage quota errors ("insufficient storage quota"),
   image pull errors (registry credentials), or IP/network errors in the failing conditions.
4. Compare storage used with the quota: a rollout creates new VMs (with their disks) before old ones go.
5. Machines that keep being replaced (young age, repeated names) point at a MachineHealthCheck remediating a
   machine that cannot start; fix the cause, not the health check.
State the most likely cause first, with the evidence line, then the fix.
`),
	)
	return api.NewPromptCallResult(
		fmt.Sprintf("Troubleshooting data for VKS cluster %s/%s", namespace, name),
		[]api.PromptMessage{
			{Role: "user", Content: api.PromptContent{Type: "text", Text: b.String()}},
			{Role: "assistant", Content: api.PromptContent{Type: "text", Text: troubleshootAssistantText}},
		},
		nil,
	), nil
}

// notTrue lists the conditions that are not True, with their message, skipping
// conditions where False is the healthy state.
func notTrue(obj map[string]any, indent string) string {
	healthyWhenFalse := map[string]bool{"Deleting": true, "Paused": true, "Updating": true, "RollingOut": true,
		"Remediating": true, "ScalingDown": true, "ScalingUp": true}
	conditions, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	var b strings.Builder
	for _, c := range conditions {
		m, _ := c.(map[string]any)
		t, _ := m["type"].(string)
		if m["status"] == "True" || healthyWhenFalse[t] {
			continue
		}
		msg := oneLine(m["message"])
		if msg == "" {
			msg = oneLine(m["reason"])
		}
		fmt.Fprintf(&b, "%s%s=%v: %s\n", indent, t, m["status"], shorten(msg, 400))
	}
	return b.String()
}
