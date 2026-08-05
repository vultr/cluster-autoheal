package vultr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vultr/cluster-autoheal/internal/cloudprovider"
	"github.com/vultr/govultr/v3"
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

func TestAlertNodeCreatesTicket(t *testing.T) {
	var ticket createTicketRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != ticketsPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, ticketsPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&ticket); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := govultr.NewClient(server.Client())
	if err := client.SetBaseURL(server.URL); err != nil {
		t.Fatalf("set base url: %v", err)
	}
	provider := &Provider{client: client}
	node := &corev1.Node{}
	node.Name = "worker-1"
	node.Labels = map[string]string{nodeIDLabel: "node-123"}
	firstSeen := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)

	err := provider.AlertNode(context.Background(), node, cloudprovider.NodeAlert{
		Condition: corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionFalse, Reason: "KubeletNotReady", Message: "kubelet stopped"},
		Action:    cloudprovider.NodeRepairReplace,
		FirstSeen: firstSeen,
	})
	if err != nil {
		t.Fatalf("AlertNode() error = %v", err)
	}
	if ticket.SubUUID != "node-123" {
		t.Fatalf("sub uuid = %q, want node-123", ticket.SubUUID)
	}
	if ticket.Subject == "" {
		t.Fatal("subject is empty")
	}
	if ticket.Description == "" {
		t.Fatal("description is empty")
	}
}

func TestAlertNodeRequiresVultrIdentifier(t *testing.T) {
	provider := &Provider{}
	node := &corev1.Node{}
	node.Name = "worker-1"

	if err := provider.AlertNode(context.Background(), node, cloudprovider.NodeAlert{}); err == nil {
		t.Fatal("AlertNode() error = nil, want error")
	}
}
