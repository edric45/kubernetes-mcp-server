package vks

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
)

// CatalogNamespace holds the VKS catalog that every Supervisor user can read:
// ClusterClasses, AddonReleases and their AddonConfigDefinitions.
const CatalogNamespace = "vmware-system-vks-public"

// unlimitedQuota is the storage quota limit the Supervisor reports for "no limit".
const unlimitedQuota = "9223372036854775807"

// kind names a resource by group and kind; the version is resolved from the
// Supervisor's discovery data, so a newer API version is picked up without a
// code change. fallback is used when discovery has no answer.
type kind struct {
	group, kind, fallbackVersion, resource string
}

var (
	kubernetesReleaseKind     = kind{"kubernetes.vmware.com", "KubernetesRelease", "v1alpha1", "kubernetesreleases"}
	clusterClassKind          = kind{"cluster.x-k8s.io", "ClusterClass", "v1beta2", "clusterclasses"}
	clusterKind               = kind{"cluster.x-k8s.io", "Cluster", "v1beta2", "clusters"}
	machineKind               = kind{"cluster.x-k8s.io", "Machine", "v1beta2", "machines"}
	machineDeploymentKind     = kind{"cluster.x-k8s.io", "MachineDeployment", "v1beta2", "machinedeployments"}
	addonReleaseKind          = kind{"addons.kubernetes.vmware.com", "AddonRelease", "v1alpha1", "addonreleases"}
	addonConfigDefinitionKind = kind{"addons.kubernetes.vmware.com", "AddonConfigDefinition", "v1alpha1", "addonconfigdefinitions"}
	storagePolicyQuotaKind    = kind{"cns.vmware.com", "StoragePolicyQuota", "v1alpha2", "storagepolicyquotas"}
	storagePolicyUsageKind    = kind{"cns.vmware.com", "StoragePolicyUsage", "v1alpha2", "storagepolicyusages"}
	virtualMachineClassKind   = kind{"vmoperator.vmware.com", "VirtualMachineClass", "v1alpha5", "virtualmachineclasses"}
)

func (k kind) gvr(client api.KubernetesClient) schema.GroupVersionResource {
	if m, err := client.RESTMapper().RESTMapping(schema.GroupKind{Group: k.group, Kind: k.kind}); err == nil {
		return m.Resource
	}
	return schema.GroupVersionResource{Group: k.group, Version: k.fallbackVersion, Resource: k.resource}
}

func list(ctx context.Context, client api.KubernetesClient, k kind, namespace, labelSelector string) ([]unstructured.Unstructured, error) {
	l, err := client.DynamicClient().Resource(k.gvr(client)).Namespace(namespace).
		List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}

func get(ctx context.Context, client api.KubernetesClient, k kind, namespace, name string) (*unstructured.Unstructured, error) {
	return client.DynamicClient().Resource(k.gvr(client)).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// condition returns the status ("True", "False", "Unknown") of a condition, or "?".
func condition(obj map[string]any, conditionType string) string {
	if c := findCondition(obj, conditionType); c != nil {
		if s, ok := c["status"].(string); ok {
			return s
		}
	}
	return "?"
}

func findCondition(obj map[string]any, conditionType string) map[string]any {
	conditions, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	for _, c := range conditions {
		if m, ok := c.(map[string]any); ok && m["type"] == conditionType {
			return m
		}
	}
	return nil
}

// catalogRBAC is the read access every catalog tool needs.
func catalogRBAC(k kind, verbs ...string) api.RBACRequirement {
	r := api.RBACRequirement{
		Verbs:  verbs,
		Target: api.RBACTarget{Resource: &api.RBACResourceTarget{APIGroup: k.group, Resource: k.resource}},
	}
	if k != kubernetesReleaseKind { // KubernetesReleases are cluster-scoped
		r.Namespace = &api.RBACNamespace{Name: CatalogNamespace}
	}
	return r
}

func namespaceRBAC(k kind) api.RBACRequirement {
	return api.RBACRequirement{
		Verbs:     []string{"list"},
		Target:    api.RBACTarget{Resource: &api.RBACResourceTarget{APIGroup: k.group, Resource: k.resource}},
		Namespace: &api.RBACNamespace{Argument: "namespace"},
	}
}
