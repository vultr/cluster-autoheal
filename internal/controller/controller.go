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
	client             kubernetes.Interface
	provider           cloudprovider.Interface
	cfg                config.Config
	conditionFirstSeen map[string]time.Time
	repaired           map[string]time.Time
}

type repairCandidate struct {
	node      *corev1.Node
	condition corev1.NodeCondition
	rule      config.RepairRule
	key       string
	firstSeen time.Time
	action    cloudprovider.NodeRepairAction
}

func New(client kubernetes.Interface, provider cloudprovider.Interface, cfg config.Config) *Controller {
	return &Controller{
		client:             client,
		provider:           provider,
		cfg:                cfg,
		conditionFirstSeen: map[string]time.Time{},
		repaired:           map[string]time.Time{},
	}
}

func (c *Controller) Run(ctx context.Context) error {
	if c.cfg.ScanInterval <= 0 {
		return fmt.Errorf("scan interval must be positive")
	}
	if c.cfg.DrainTimeout <= 0 {
		return fmt.Errorf("drain timeout must be positive")
	}
	if c.cfg.RebootReadyTimeout <= 0 {
		return fmt.Errorf("reboot ready timeout must be positive")
	}

	if len(c.cfg.RepairRules) == 0 {
		return fmt.Errorf("at least one repair rule is required")
	}
	if c.cfg.ActionOverrideLabel == "" {
		return fmt.Errorf("action override label must not be empty")
	}
	for _, rule := range c.cfg.RepairRules {
		if rule.Condition == "" {
			return fmt.Errorf("repair rule condition must not be empty")
		}
		if rule.MinRepairWait.Duration <= 0 {
			return fmt.Errorf("repair rule %s min repair wait must be positive", rule.Condition)
		}
		if _, err := repairAction(rule.Action); err != nil {
			return err
		}
	}

	klog.Infof("starting cluster-autoheal with provider=%s rules=%d cordon=%t drain=%t dryRun=%t", c.provider.Name(), len(c.cfg.RepairRules), c.cfg.CordonBeforeRepair, c.cfg.DrainBeforeRepair, c.cfg.DryRun)
	if err := c.scan(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(c.cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.scan(ctx); err != nil {
				klog.Errorf("scan failed: %v", err)
			}
		}
	}
}

func (c *Controller) scan(ctx context.Context) error {
	nodes, err := c.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	now := time.Now()
	seenNodes := map[string]struct{}{}
	seenConditions := map[string]struct{}{}
	candidates := []repairCandidate{}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		seenNodes[node.Name] = struct{}{}

		if isReady(node) {
			if err := c.recoverRebootedNode(ctx, node); err != nil {
				return err
			}
		}

		if node.Spec.Unschedulable {
			c.logRebootTimeout(node, now)
		}

		if node.Spec.Unschedulable && !isControllerCordon(node) {
			continue
		}

		candidate, ok, err := c.repairCandidate(node, now, seenConditions)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		candidates = append(candidates, candidate)
	}

	for key := range c.conditionFirstSeen {
		if _, ok := seenConditions[key]; !ok {
			delete(c.conditionFirstSeen, key)
			delete(c.repaired, key)
		}
	}
	for key := range c.repaired {
		nodeName, _ := splitRepairKey(key)
		if _, ok := seenNodes[nodeName]; !ok {
			delete(c.repaired, key)
		}
	}

	if len(candidates) == 0 {
		return nil
	}
	if c.repairThresholdExceeded(len(candidates), len(nodes.Items)) {
		klog.Warningf("skipping repairs: unhealthy candidate count %d exceeds configured threshold", len(candidates))
		return nil
	}

	maxRepairs := c.maxParallelRepairs(len(candidates))
	for repairedCount, candidate := range candidates {
		if repairedCount >= maxRepairs {
			continue
		}

		if c.cfg.DryRun {
			klog.Infof("dry-run: would repair node %s condition=%s reason=%s action=%s", candidate.node.Name, candidate.condition.Type, candidate.condition.Reason, candidate.action)
			c.repaired[candidate.key] = now
			continue
		}

		if err := c.prepareNodeForRepair(ctx, candidate.node, candidate.action, now); err != nil {
			return fmt.Errorf("prepare node %s for repair: %w", candidate.node.Name, err)
		}

		klog.Infof("repairing node %s condition=%s reason=%s action=%s", candidate.node.Name, candidate.condition.Type, candidate.condition.Reason, candidate.action)
		if err := c.provider.RepairNode(ctx, candidate.node, candidate.action); err != nil {
			return fmt.Errorf("repair node %s: %w", candidate.node.Name, err)
		}
		c.repaired[candidate.key] = now
	}

	return nil
}

func (c *Controller) repairCandidate(node *corev1.Node, now time.Time, seen map[string]struct{}) (repairCandidate, bool, error) {
	for _, condition := range node.Status.Conditions {
		rule, ok := c.matchingRule(condition)
		if !ok || conditionHealthy(condition) {
			continue
		}

		key := repairKey(node.Name, condition.Type, condition.Reason)
		seen[key] = struct{}{}
		firstSeen, ok := c.conditionFirstSeen[key]
		if !ok {
			c.conditionFirstSeen[key] = now
			klog.Infof("node %s condition=%s reason=%s is unhealthy; waiting %s before repair", node.Name, condition.Type, condition.Reason, rule.MinRepairWait)
			continue
		}
		if now.Sub(firstSeen) < rule.MinRepairWait.Duration {
			continue
		}
		if repairedAt, ok := c.repaired[key]; ok && !repairedAt.Before(firstSeen) {
			continue
		}

		actionName := rule.Action
		if override := node.Labels[c.cfg.ActionOverrideLabel]; override != "" {
			actionName = override
		}

		action, err := repairAction(actionName)
		if err != nil {
			return repairCandidate{}, false, err
		}
		if action == "" {
			c.repaired[key] = now
			klog.Infof("node %s condition=%s reason=%s matched no-action repair", node.Name, condition.Type, condition.Reason)
			continue
		}

		return repairCandidate{node: node, condition: condition, rule: rule, key: key, firstSeen: firstSeen, action: action}, true, nil
	}

	return repairCandidate{}, false, nil
}

func (c *Controller) matchingRule(condition corev1.NodeCondition) (config.RepairRule, bool) {
	var fallback config.RepairRule
	hasFallback := false
	for _, rule := range c.cfg.RepairRules {
		if rule.Condition != string(condition.Type) {
			continue
		}
		if rule.Reason != "" && rule.Reason == condition.Reason {
			return rule, true
		}
		if rule.Reason == "" && !hasFallback {
			fallback = rule
			hasFallback = true
		}
	}

	return fallback, hasFallback
}

func conditionHealthy(condition corev1.NodeCondition) bool {
	if condition.Type == corev1.NodeReady {
		return condition.Status == corev1.ConditionTrue
	}

	return condition.Status == corev1.ConditionTrue
}

func (c *Controller) repairThresholdExceeded(unhealthyCandidates, totalNodes int) bool {
	if c.cfg.MaxUnhealthyNodeThresholdCount > 0 && unhealthyCandidates > c.cfg.MaxUnhealthyNodeThresholdCount {
		return true
	}
	if c.cfg.MaxUnhealthyNodeThresholdPercentage > 0 && percentage(unhealthyCandidates, totalNodes) > c.cfg.MaxUnhealthyNodeThresholdPercentage {
		return true
	}

	return false
}

func (c *Controller) maxParallelRepairs(unhealthyCandidates int) int {
	if c.cfg.MaxParallelNodesRepairedCount > 0 {
		return c.cfg.MaxParallelNodesRepairedCount
	}
	if c.cfg.MaxParallelNodesRepairedPercentage > 0 {
		max := percentageCount(unhealthyCandidates, c.cfg.MaxParallelNodesRepairedPercentage)
		if max > 0 {
			return max
		}
	}

	return 1
}

func percentage(count, total int) int {
	if total <= 0 {
		return 0
	}

	return count * 100 / total
}

func percentageCount(total, pct int) int {
	if total <= 0 || pct <= 0 {
		return 0
	}

	count := total * pct / 100
	if count == 0 {
		return 1
	}

	return count
}

func repairKey(nodeName string, conditionType corev1.NodeConditionType, reason string) string {
	return fmt.Sprintf("%s|%s|%s", nodeName, conditionType, reason)
}

func splitRepairKey(key string) (string, string) {
	for i := range key {
		if key[i] == '|' {
			return key[:i], key[i+1:]
		}
	}

	return key, ""
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
	case config.ActionNoAction:
		return "", nil
	case config.ActionReboot:
		return cloudprovider.NodeRepairReboot, nil
	case config.ActionReplace:
		return cloudprovider.NodeRepairReplace, nil
	default:
		return "", fmt.Errorf("unknown repair action %q", action)
	}
}
