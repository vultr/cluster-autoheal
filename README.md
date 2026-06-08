# cluster-autoheal

`cluster-autoheal` is a cloud-agnostic Kubernetes node auto-healing controller. It watches node readiness and asks a registered cloud provider implementation to repair nodes that remain unhealthy for longer than the configured threshold.

The controller core does not know how a cloud repairs machines. Providers implement a small interface and are registered by name, following the same general split used by Kubernetes components such as cluster-autoscaler and cloud-controller-manager.

## Current Status

This is an initial controller foundation with:

- A Go controller binary at `cmd/cluster-autoheal`.
- A cloud provider registry in `internal/cloudprovider`.
- A polling node-health controller in `internal/controller`.
- A Vultr provider in `internal/cloudprovider/vultr`.
- `reboot` and `replace` repair actions.

For Vultr, `reboot` reboots the resource backing the node. `replace` deletes the resource; replacement is expected to be handled by the cluster's node provisioning layer. The provider supports both Vultr cloud compute instances and bare metal servers.

## Running Locally

```sh
export VULTR_API_KEY=...
go run ./cmd/cluster-autoheal \
  --kubeconfig ~/.kube/config \
  --cloud-provider vultr \
  --repair-action reboot \
  --unhealthy-duration 10m \
  --scan-interval 30s \
  --dry-run
```

## Flags

- `--cloud-provider`: provider implementation to use. Defaults to `vultr`.
- `--kubeconfig`: path to a kubeconfig. Empty uses in-cluster config.
- `--scan-interval`: how often to scan node health. Defaults to `30s`.
- `--unhealthy-duration`: how long a node must remain unhealthy before repair. Defaults to `10m`.
- `--repair-action`: `reboot` or `replace`. Defaults to `reboot`.
- `--dry-run`: log repair decisions without changing cloud resources.

## Provider Contract

Providers implement:

```go
type Interface interface {
    Name() string
    RepairNode(ctx context.Context, node *corev1.Node, action NodeRepairAction) error
}
```

New providers should register themselves from their package `init` function using `cloudprovider.Register`.

## Vultr Node Detection

The Vultr provider detects the backing resource from VKE node labels when available:

- `vke.vultr.com/node-id`: Vultr resource ID.
- `vultr.com/baremetal`: `true` means use the bare metal API; any other value uses the instance API.

If `vke.vultr.com/node-id` is missing, the provider falls back to Kubernetes `spec.providerID` values prefixed with `vultr://` or `vultr/`.
