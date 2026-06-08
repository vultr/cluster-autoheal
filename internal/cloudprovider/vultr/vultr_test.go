package vultr

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestResourceForNodeUsesVKELabels(t *testing.T) {
	node := &corev1.Node{}
	node.Name = "worker-1"
	node.Labels = map[string]string{
		nodeIDLabel:        "abc123",
		bareMetalNodeLabel: "true",
	}
	node.Spec.ProviderID = "vultr://ignored"

	resource, err := resourceForNode(node)
	if err != nil {
		t.Fatalf("resourceForNode() error = %v", err)
	}
	if resource.id != "abc123" {
		t.Fatalf("resource id = %q, want abc123", resource.id)
	}
	if resource.typeName != resourceTypeBareMetal {
		t.Fatalf("resource type = %q, want %q", resource.typeName, resourceTypeBareMetal)
	}
}

func TestResourceForNodeDefaultsToInstance(t *testing.T) {
	node := &corev1.Node{}
	node.Name = "worker-1"
	node.Labels = map[string]string{
		nodeIDLabel:        "abc123",
		bareMetalNodeLabel: "false",
	}

	resource, err := resourceForNode(node)
	if err != nil {
		t.Fatalf("resourceForNode() error = %v", err)
	}
	if resource.typeName != resourceTypeInstance {
		t.Fatalf("resource type = %q, want %q", resource.typeName, resourceTypeInstance)
	}
}

func TestResourceForNodeFallsBackToProviderID(t *testing.T) {
	node := &corev1.Node{}
	node.Name = "worker-1"
	node.Labels = map[string]string{}
	node.Spec.ProviderID = "vultr://abc123"

	resource, err := resourceForNode(node)
	if err != nil {
		t.Fatalf("resourceForNode() error = %v", err)
	}
	if resource.id != "abc123" {
		t.Fatalf("resource id = %q, want abc123", resource.id)
	}
	if resource.typeName != resourceTypeInstance {
		t.Fatalf("resource type = %q, want %q", resource.typeName, resourceTypeInstance)
	}
}

func TestResourceForNodeRequiresVultrIdentifier(t *testing.T) {
	node := &corev1.Node{}
	node.Name = "worker-1"
	node.Labels = map[string]string{}
	node.Spec.ProviderID = "aws://abc123"

	if _, err := resourceForNode(node); err == nil {
		t.Fatal("resourceForNode() error = nil, want error")
	}
}
