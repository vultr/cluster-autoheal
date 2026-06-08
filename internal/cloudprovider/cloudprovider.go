package cloudprovider

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
)

type NodeRepairAction string

const (
	NodeRepairReboot  NodeRepairAction = "reboot"
	NodeRepairReplace NodeRepairAction = "replace"
)

type Interface interface {
	Name() string
	RepairNode(ctx context.Context, node *corev1.Node, action NodeRepairAction) error
}

type Builder func() (Interface, error)

var (
	buildersMu sync.RWMutex
	builders   = map[string]Builder{}
)

func Register(name string, builder Builder) {
	buildersMu.Lock()
	defer buildersMu.Unlock()

	if name == "" {
		panic("cloud provider name must not be empty")
	}
	if builder == nil {
		panic("cloud provider builder must not be nil")
	}
	if _, exists := builders[name]; exists {
		panic(fmt.Sprintf("cloud provider %q already registered", name))
	}

	builders[name] = builder
}

func Build(name string) (Interface, error) {
	buildersMu.RLock()
	builder, ok := builders[name]
	buildersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown cloud provider %q", name)
	}

	return builder()
}
