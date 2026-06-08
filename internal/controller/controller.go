package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/vultr/cluster-autoheal/internal/cloudprovider"
	"github.com/vultr/cluster-autoheal/internal/config"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	cordonedByAnnotation      = "cluster-autoheal.vultr.com/cordoned-by"
	repairActionAnnotation    = "cluster-autoheal.vultr.com/repair-action"
	repairStartedAnnotation   = "cluster-autoheal.vultr.com/repair-started-at"
	cordonedByAnnotationValue = "cluster-autoheal"
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
	if c.cfg.DrainTimeout <= 0 {
		return fmt.Errorf("drain timeout must be positive")
	}
	if c.cfg.RebootReadyTimeout <= 0 {
		return fmt.Errorf("reboot ready timeout must be positive")
	}

	action, err := repairAction(c.cfg.RepairAction)
	if err != nil {
		return err
	}

	klog.Infof("starting cluster-autoheal with provider=%s action=%s cordon=%t drain=%t dryRun=%t", c.provider.Name(), action, c.cfg.CordonBeforeRepair, c.cfg.DrainBeforeRepair, c.cfg.DryRun)
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

		if isReady(node) {
			if err := c.recoverRebootedNode(ctx, node); err != nil {
				return err
			}
			delete(c.firstSeen, node.Name)
			continue
		}

		if node.Spec.Unschedulable {
			c.logRebootTimeout(node, now)
		}

		if node.Spec.Unschedulable && !isControllerCordon(node) {
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
			klog.Infof("dry-run: would prepare and repair node %s with action %s", node.Name, action)
			c.repaired[node.Name] = now
			continue
		}

		if err := c.prepareNodeForRepair(ctx, node, action, now); err != nil {
			return fmt.Errorf("prepare node %s for repair: %w", node.Name, err)
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

func (c *Controller) prepareNodeForRepair(ctx context.Context, node *corev1.Node, action cloudprovider.NodeRepairAction, now time.Time) error {
	if c.cfg.CordonBeforeRepair {
		if err := c.cordonNode(ctx, node.Name, action, now); err != nil {
			return err
		}
	}

	if c.cfg.DrainBeforeRepair {
		if !c.cfg.CordonBeforeRepair {
			if err := c.cordonNode(ctx, node.Name, action, now); err != nil {
				return err
			}
		}
		return c.drainNode(ctx, node.Name)
	}

	return nil
}

func (c *Controller) cordonNode(ctx context.Context, nodeName string, action cloudprovider.NodeRepairAction, now time.Time) error {
	node, err := c.client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[cordonedByAnnotation] = cordonedByAnnotationValue
	node.Annotations[repairActionAnnotation] = string(action)
	node.Annotations[repairStartedAnnotation] = now.UTC().Format(time.RFC3339)
	node.Spec.Unschedulable = true

	_, err = c.client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	klog.Infof("cordoned node %s before %s repair", nodeName, action)
	return nil
}

func (c *Controller) recoverRebootedNode(ctx context.Context, node *corev1.Node) error {
	if !c.cfg.UncordonAfterReboot || !isControllerCordon(node) || node.Annotations[repairActionAnnotation] != string(cloudprovider.NodeRepairReboot) {
		return nil
	}

	freshNode, err := c.client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !isControllerCordon(freshNode) || freshNode.Annotations[repairActionAnnotation] != string(cloudprovider.NodeRepairReboot) {
		return nil
	}

	freshNode.Spec.Unschedulable = false
	delete(freshNode.Annotations, cordonedByAnnotation)
	delete(freshNode.Annotations, repairActionAnnotation)
	delete(freshNode.Annotations, repairStartedAnnotation)

	_, err = c.client.CoreV1().Nodes().Update(ctx, freshNode, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	klog.Infof("uncordoned node %s after reboot repair returned Ready", node.Name)
	return nil
}

func (c *Controller) logRebootTimeout(node *corev1.Node, now time.Time) {
	if !isControllerCordon(node) || node.Annotations[repairActionAnnotation] != string(cloudprovider.NodeRepairReboot) {
		return
	}

	startedAt, err := time.Parse(time.RFC3339, node.Annotations[repairStartedAnnotation])
	if err != nil || now.Sub(startedAt) <= c.cfg.RebootReadyTimeout {
		return
	}

	klog.Warningf("node %s has not returned Ready %s after reboot repair", node.Name, c.cfg.RebootReadyTimeout)
}

func (c *Controller) drainNode(ctx context.Context, nodeName string) error {
	deadline := time.Now().Add(c.cfg.DrainTimeout)
	for {
		remaining, err := c.evictablePods(ctx, nodeName)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			klog.Infof("drained node %s", nodeName)
			return nil
		}

		for i := range remaining {
			pod := remaining[i]
			if err := c.evictPod(ctx, &pod); err != nil {
				return err
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timed out draining node %s", nodeName)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Controller) evictablePods(ctx context.Context, nodeName string) ([]corev1.Pod, error) {
	selector := fields.OneTermEqualSelector("spec.nodeName", nodeName).String()
	pods, err := c.client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return nil, err
	}

	remaining := make([]corev1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		pod := pods.Items[i]
		if skipDrainPod(&pod) {
			continue
		}
		if !c.cfg.DeleteEmptyDirData && hasEmptyDir(&pod) {
			return nil, fmt.Errorf("pod %s/%s uses emptyDir; set --delete-emptydir-data to allow drain", pod.Namespace, pod.Name)
		}
		remaining = append(remaining, pod)
	}

	return remaining, nil
}

func (c *Controller) evictPod(ctx context.Context, pod *corev1.Pod) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	err := c.client.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
	if apierrors.IsNotFound(err) || apierrors.IsTooManyRequests(err) {
		return nil
	}
	if err != nil {
		return err
	}

	klog.Infof("evicted pod %s/%s", pod.Namespace, pod.Name)
	return nil
}

func skipDrainPod(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return true
	}
	if _, ok := pod.Annotations[corev1.MirrorPodAnnotationKey]; ok {
		return true
	}
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			return true
		}
	}

	return false
}

func hasEmptyDir(pod *corev1.Pod) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.EmptyDir != nil {
			return true
		}
	}

	return false
}

func isControllerCordon(node *corev1.Node) bool {
	return node.Annotations[cordonedByAnnotation] == cordonedByAnnotationValue
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
