// Package vks provides tools for the VMware vSphere Supervisor and vSphere
// Kubernetes Service (VKS): the VKS catalog (Kubernetes releases, ClusterClasses,
// add-ons), what a ClusterClass or add-on lets you configure, and the state of
// clusters and capacity in a vSphere Namespace.
//
// Run the server with a kubeconfig context (or in-cluster ServiceAccount) for the
// Supervisor, not for a workload cluster.
package vks

import (
	"context"
	"slices"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
	"github.com/containers/kubernetes-mcp-server/pkg/toolsets"
)

type Toolset struct{}

var _ api.Toolset = (*Toolset)(nil)

func (t *Toolset) GetName() string {
	return "vks"
}

func (t *Toolset) GetDescription() string {
	return "Tools for the vSphere Supervisor and vSphere Kubernetes Service (VKS): catalog, cluster settings, namespace overview"
}

func (t *Toolset) GetTools(_ context.Context, _ api.ToolsetContext) []api.ServerTool {
	return slices.Concat(
		initCatalog(),
		initSettings(),
		initNamespace(),
	)
}

func (t *Toolset) GetPrompts(_ context.Context, _ api.ToolsetContext) []api.ServerPrompt {
	return initTroubleshoot()
}

func (t *Toolset) GetResources(_ context.Context, _ api.ToolsetContext) []api.ServerResource {
	return nil
}

func (t *Toolset) GetResourceTemplates(_ context.Context, _ api.ToolsetContext) []api.ServerResourceTemplate {
	return nil
}

func init() {
	toolsets.Register(&Toolset{})
}
