package vultr

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/vultr/cluster-autoheal/internal/cloudprovider"
	"github.com/vultr/govultr/v3"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
)

const (
	providerName       = "vultr"
	nodeIDLabel        = "vke.vultr.com/node-id"
	bareMetalNodeLabel = "vultr.com/baremetal"
	ticketsPath        = "/v2/tickets"
)

type createTicketRequest struct {
	Subject     string `json:"subject"`
	Description string `json:"description"`
	SubUUID     string `json:"sub-uuid"`
}

type resourceType string

const (
	resourceTypeInstance  resourceType = "instance"
	resourceTypeBareMetal resourceType = "baremetal"
)

type nodeResource struct {
	typeName resourceType
	id       string
}

type Provider struct {
	client *govultr.Client
}

func init() {
	cloudprovider.Register(providerName, New)
}

func New() (cloudprovider.Interface, error) {
	apiKey := os.Getenv("VULTR_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("VULTR_API_KEY is required")
	}

	ctx := context.Background()
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: apiKey})
	client := govultr.NewClient(oauth2.NewClient(ctx, tokenSource))

	return &Provider{client: client}, nil
}

func (p *Provider) Name() string {
	return providerName
}

func (p *Provider) RepairNode(ctx context.Context, node *corev1.Node, action cloudprovider.NodeRepairAction) error {
	resource, err := resourceForNode(node)
	if err != nil {
		return err
	}

	switch action {
	case cloudprovider.NodeRepairReboot:
		if resource.typeName == resourceTypeBareMetal {
			return p.client.BareMetalServer.Reboot(ctx, resource.id)
		}
		return p.client.Instance.Reboot(ctx, resource.id)
	case cloudprovider.NodeRepairReplace:
		if resource.typeName == resourceTypeBareMetal {
			return p.client.BareMetalServer.Delete(ctx, resource.id)
		}
		return p.client.Instance.Delete(ctx, resource.id)
	default:
		return fmt.Errorf("unsupported repair action %q", action)
	}
}

func (p *Provider) AlertNode(ctx context.Context, node *corev1.Node, alert cloudprovider.NodeAlert) error {
	resource, err := resourceForNode(node)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("cluster-autoheal: node %s requires attention", node.Name)
	description := fmt.Sprintf("cluster-autoheal detected an unhealthy node.\n\nNode: %s\nVultr resource: %s %s\nCondition: %s\nStatus: %s\nReason: %s\nMessage: %s\nFirst seen: %s\nRepair action: %s",
		node.Name,
		resource.typeName,
		resource.id,
		alert.Condition.Type,
		alert.Condition.Status,
		alert.Condition.Reason,
		alert.Condition.Message,
		alert.FirstSeen.UTC().Format("2006-01-02T15:04:05Z"),
		alert.Action,
	)

	return p.createTicket(ctx, subject, description, resource.id)
}

func (p *Provider) createTicket(ctx context.Context, subject, description, subUUID string) error {
	req, err := p.client.NewRequest(ctx, http.MethodPost, ticketsPath, createTicketRequest{
		Subject:     subject,
		Description: description,
		SubUUID:     subUUID,
	})
	if err != nil {
		return err
	}

	_, err = p.client.DoWithContext(ctx, req, nil)
	return err
}

func resourceForNode(node *corev1.Node) (nodeResource, error) {
	if id := strings.TrimSpace(node.Labels[nodeIDLabel]); id != "" {
		return nodeResource{typeName: nodeResourceType(node), id: id}, nil
	}

	providerID := strings.TrimSpace(node.Spec.ProviderID)
	if providerID == "" {
		return nodeResource{}, fmt.Errorf("node %s has neither %s label nor providerID", node.Name, nodeIDLabel)
	}

	for _, prefix := range []string{"vultr://", "vultr/"} {
		if strings.HasPrefix(providerID, prefix) {
			id := strings.TrimPrefix(providerID, prefix)
			if id == "" {
				break
			}
			return nodeResource{typeName: nodeResourceType(node), id: id}, nil
		}
	}

	return nodeResource{}, fmt.Errorf("node %s providerID %q is not a Vultr providerID", node.Name, providerID)
}

func nodeResourceType(node *corev1.Node) resourceType {
	if strings.EqualFold(strings.TrimSpace(node.Labels[bareMetalNodeLabel]), "true") {
		return resourceTypeBareMetal
	}

	return resourceTypeInstance
}
