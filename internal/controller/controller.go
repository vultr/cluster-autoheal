package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/vultr/cluster-autoheal/internal/cloudprovider"
	"github.com/vultr/cluster-autoheal/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type Controller struct {
	client    kubernetes.Interface
	provider  cloudprovider.Interface
	cfg       config.Config
	firstSeen map[string]time.Time
	repaired  map[string]time.Time
}

func New(client kubernetes.Interface, provider cloudprovider.Interface, cfg config.Config) *Controller {
	return &Controller{
		client:    client,
		provider:  provider,
		cfg:       cfg,
		firstSeen: map[string]time.Time{},
		repaired:  map[string]time.Time{},
	}
}

func (c *Controller) Run(ctx context.Context) error {
	if c.cfg.ScanInterval <= 0 {
		return fmt.Errorf("scan interval must be positive")
	}
	if c.cfg.UnhealthyDuration <= 0 {
		return fmt.Errorf("unhealthy duration must be positive")
	}

	action, err := repairAction(c.cfg.RepairAction)
	if err != nil {
		return err
	}

	klog.Infof("starting cluster-autoheal with provider=%s action=%s dryRun=%t", c.provider.Name(), action, c.cfg.DryRun)
	if err := c.scan(ctx, action); err != nil {
		return err
	}

	ticker := time.NewTicker(c.cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.scan(ctx, action); err != nil {
				klog.Errorf("scan failed: %v", err)
			}
		}
	}
}

func (c *Controller) scan(ctx context.Context, action cloudprovider.NodeRepairAction) error {
	nodes, err := c.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	now := time.Now()
	seen := map[string]struct{}{}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		seen[node.Name] = struct{}{}

		if isReady(node) || node.Spec.Unschedulable {
			delete(c.firstSeen, node.Name)
			continue
		}

		firstSeen, ok := c.firstSeen[node.Name]
		if !ok {
			c.firstSeen[node.Name] = now
			klog.Infof("node %s is unhealthy; waiting %s before repair", node.Name, c.cfg.UnhealthyDuration)
			continue
		}

		if now.Sub(firstSeen) < c.cfg.UnhealthyDuration {
			continue
		}

		if repairedAt, ok := c.repaired[node.Name]; ok && !repairedAt.Before(firstSeen) {
			continue
		}

		if c.cfg.DryRun {
			klog.Infof("dry-run: would repair node %s with action %s", node.Name, action)
			c.repaired[node.Name] = now
			continue
		}

		klog.Infof("repairing node %s with action %s", node.Name, action)
		if err := c.provider.RepairNode(ctx, node, action); err != nil {
			return fmt.Errorf("repair node %s: %w", node.Name, err)
		}
		c.repaired[node.Name] = now
	}

	for nodeName := range c.firstSeen {
		if _, ok := seen[nodeName]; !ok {
			delete(c.firstSeen, nodeName)
			delete(c.repaired, nodeName)
		}
	}

	return nil
}

func isReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}

func repairAction(action string) (cloudprovider.NodeRepairAction, error) {
	switch action {
	case config.ActionReboot:
		return cloudprovider.NodeRepairReboot, nil
	case config.ActionReplace:
		return cloudprovider.NodeRepairReplace, nil
	default:
		return "", fmt.Errorf("unknown repair action %q", action)
	}
}
