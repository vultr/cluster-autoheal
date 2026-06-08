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
  --health-addr :8080 \
  --repair-action reboot \
  --unhealthy-duration 10m \
  --scan-interval 30s \
  --dry-run
```

## Installing With Helm

Create a Kubernetes secret for the Vultr API key:

```sh
kubectl create secret generic cluster-autoheal-vultr \
  --from-literal=api-key="$VULTR_API_KEY" \
  --namespace kube-system
```

Install the chart:

```sh
helm install cluster-autoheal ./charts/cluster-autoheal \
  --namespace kube-system \
  --set vultr.existingSecret=cluster-autoheal-vultr
```

For a safe first run, enable dry-run mode:

```sh
helm upgrade --install cluster-autoheal ./charts/cluster-autoheal \
  --namespace kube-system \
  --set vultr.existingSecret=cluster-autoheal-vultr \
  --set controller.dryRun=true
```

## Flags

- `--cloud-provider`: provider implementation to use. Defaults to `vultr`.
- `--kubeconfig`: path to a kubeconfig. Empty uses in-cluster config.
- `--health-addr`: address for `/healthz`, `/readyz`, and `/version`. Defaults to `:8080`.
- `--scan-interval`: how often to scan node health. Defaults to `30s`.
- `--unhealthy-duration`: how long a node must remain unhealthy before repair. Defaults to `10m`.
- `--drain-timeout`: maximum time to wait for pod evictions during drain. Defaults to `10m`.
- `--reboot-ready-timeout`: time after which a controller-cordoned reboot is reported as overdue. Defaults to `15m`.
- `--repair-action`: `reboot` or `replace`. Defaults to `reboot`.
- `--cordon-before-repair`: cordon nodes before repair. Defaults to `true`.
- `--drain-before-repair`: evict drainable pods before repair. Defaults to `false`.
- `--uncordon-after-reboot`: uncordon controller-cordoned rebooted nodes after they return Ready. Defaults to `true`.
- `--delete-emptydir-data`: allow draining pods that use `emptyDir` volumes. Defaults to `false`.
- `--dry-run`: log repair decisions without changing cloud resources.

## Repair Lifecycle

When a node remains unhealthy beyond `--unhealthy-duration`, the controller can prepare it before calling the cloud provider:

- Cordon: enabled by default with `--cordon-before-repair=true`.
- Drain: optional with `--drain-before-repair=true`.
- Repair: calls the provider with `reboot` or `replace`.

Drain uses Kubernetes pod evictions. It skips mirror pods, DaemonSet-managed pods, completed pods, and pods already being deleted. Pods using `emptyDir` block drain unless `--delete-emptydir-data=true` is set.

For reboot repairs, the controller annotates nodes it cordons. When the same node returns Ready, `--uncordon-after-reboot=true` removes only the controller's annotations and uncordons the node. It does not uncordon nodes that were cordoned by an operator or another controller.

## Production Notes

- Run one replica for now. The controller keeps in-memory repair state and does not yet implement leader election.
- Prefer `vultr.existingSecret` over putting API keys in Helm values.
- The Helm chart grants node update permissions for cordon/uncordon and pod eviction permissions for optional drain.
- The container runs as non-root on a distroless base image with a read-only root filesystem.
- Start with `controller.dryRun=true` to validate node detection and thresholds before enabling repairs.

## Development

Useful local commands:

```sh
make lint
make test
make build
make helm-lint
make helm-template
make image TAG=dev
```

GitHub Actions run Go formatting, vet, tests, binary build, Helm lint/template, and Docker image builds. Images are published to `ghcr.io/vultr/cluster-autoheal` on pushes to `main` and version tags.

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
