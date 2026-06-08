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

type noopProvider struct{}

func (noopProvider) Name() string {
	return "noop"
}

func (noopProvider) RepairNode(context.Context, *corev1.Node, cloudprovider.NodeRepairAction) error {
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
	c := New(client, noopProvider{}, testConfig())

	if err := c.scan(context.Background(), cloudprovider.NodeRepairReboot); err != nil {
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
	c := New(client, noopProvider{}, testConfig())

	if err := c.scan(context.Background(), cloudprovider.NodeRepairReboot); err != nil {
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

func testConfig() config.Config {
	cfg := config.Default()
	cfg.UncordonAfterReboot = true
	return cfg
}
