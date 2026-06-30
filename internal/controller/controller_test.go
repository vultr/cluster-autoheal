package controller

import (
	"context"
	"testing"
	"time"

	"github.com/vultr/cluster-autoheal/internal/cloudprovider"
	"github.com/vultr/cluster-autoheal/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type recordingProvider struct {
	actions []cloudprovider.NodeRepairAction
}

func (recordingProvider) Name() string {
	return "noop"
}

func (p *recordingProvider) RepairNode(_ context.Context, _ *corev1.Node, action cloudprovider.NodeRepairAction) error {
	p.actions = append(p.actions, action)
	return nil
}

func TestScanUncordonsControllerRebootNodeWhenReady(t *testing.T) {
	node := readyNode("worker-1")
	node.Spec.Unschedulable = true
	node.Annotations = map[string]string{
		cordonedByAnnotation:    cordonedByAnnotationValue,
		repairActionAnnotation:  string(cloudprovider.NodeRepairReboot),
		repairStartedAnnotation: time.Now().UTC().Format(time.RFC3339),
	}

	client := fake.NewSimpleClientset(node)
	c := New(client, &recordingProvider{}, testConfig())

	if err := c.scan(context.Background()); err != nil {
		t.Fatalf("scan() error = %v", err)
	}

	updated, err := client.CoreV1().Nodes().Get(context.Background(), node.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if updated.Spec.Unschedulable {
		t.Fatal("node is still cordoned")
	}
	if updated.Annotations[cordonedByAnnotation] != "" {
		t.Fatalf("cordon annotation = %q, want empty", updated.Annotations[cordonedByAnnotation])
	}
}

func TestScanDoesNotUncordonExternalNodeWhenReady(t *testing.T) {
	node := readyNode("worker-1")
	node.Spec.Unschedulable = true
	node.Annotations = map[string]string{}

	client := fake.NewSimpleClientset(node)
	c := New(client, &recordingProvider{}, testConfig())

	if err := c.scan(context.Background()); err != nil {
		t.Fatalf("scan() error = %v", err)
	}

	updated, err := client.CoreV1().Nodes().Get(context.Background(), node.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if !updated.Spec.Unschedulable {
		t.Fatal("externally cordoned node was uncordoned")
	}
}

func TestScanRepairsMatchingConditionAfterRuleWait(t *testing.T) {
	node := unhealthyNode("worker-1", corev1.NodeReady, "KubeletNotReady")
	provider := &recordingProvider{}
	client := fake.NewSimpleClientset(node)
	c := New(client, provider, testConfig())
	c.conditionFirstSeen[repairKey(node.Name, corev1.NodeReady, "KubeletNotReady")] = time.Now().Add(-31 * time.Minute)

	if err := c.scan(context.Background()); err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(provider.actions) != 1 {
		t.Fatalf("repair actions = %d, want 1", len(provider.actions))
	}
	if provider.actions[0] != cloudprovider.NodeRepairReplace {
		t.Fatalf("repair action = %s, want %s", provider.actions[0], cloudprovider.NodeRepairReplace)
	}
}

func TestScanHonorsNoActionReasonOverride(t *testing.T) {
	node := unhealthyNode("worker-1", "AcceleratedHardwareReady", "NvidiaXID31Error")
	provider := &recordingProvider{}
	cfg := testConfig()
	cfg.RepairRules = []config.RepairRule{
		{Condition: "AcceleratedHardwareReady", Reason: "NvidiaXID31Error", MinRepairWait: config.Duration{Duration: time.Minute}, Action: config.ActionNoAction},
		{Condition: "AcceleratedHardwareReady", MinRepairWait: config.Duration{Duration: time.Minute}, Action: config.ActionReboot},
	}
	client := fake.NewSimpleClientset(node)
	c := New(client, provider, cfg)
	c.conditionFirstSeen[repairKey(node.Name, "AcceleratedHardwareReady", "NvidiaXID31Error")] = time.Now().Add(-2 * time.Minute)

	if err := c.scan(context.Background()); err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(provider.actions) != 0 {
		t.Fatalf("repair actions = %d, want 0", len(provider.actions))
	}
}

func TestScanHonorsNodeLabelActionOverride(t *testing.T) {
	node := unhealthyNode("worker-1", corev1.NodeReady, "KubeletNotReady")
	node.Labels = map[string]string{
		"cluster-autoheal.vultr.com/repair-action": config.ActionReboot,
	}
	provider := &recordingProvider{}
	client := fake.NewSimpleClientset(node)
	c := New(client, provider, testConfig())
	c.conditionFirstSeen[repairKey(node.Name, corev1.NodeReady, "KubeletNotReady")] = time.Now().Add(-31 * time.Minute)

	if err := c.scan(context.Background()); err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(provider.actions) != 1 {
		t.Fatalf("repair actions = %d, want 1", len(provider.actions))
	}
	if provider.actions[0] != cloudprovider.NodeRepairReboot {
		t.Fatalf("repair action = %s, want %s", provider.actions[0], cloudprovider.NodeRepairReboot)
	}
}

func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func unhealthyNode(name string, conditionType corev1.NodeConditionType, reason string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: conditionType, Status: corev1.ConditionFalse, Reason: reason},
			},
		},
	}
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.UncordonAfterReboot = true
	cfg.MaxUnhealthyNodeThresholdPercentage = 0
	return cfg
}
