# cluster-autoheal Helm Chart

Install `cluster-autoheal` into a Kubernetes cluster.

## Vultr Secret

Recommended:

```sh
kubectl create secret generic cluster-autoheal-vultr \
  --from-literal=api-key="$VULTR_API_KEY" \
  --namespace kube-system
```

```sh
helm install cluster-autoheal ./charts/cluster-autoheal \
  --namespace kube-system \
  --set vultr.existingSecret=cluster-autoheal-vultr
```

## Important Values

- `controller.cloudProvider`: provider name. Defaults to `vultr`.
- `controller.repairAction`: `reboot` or `replace`.
- `controller.cordonBeforeRepair`: cordon nodes before repair. Defaults to `true`.
- `controller.drainBeforeRepair`: drain nodes with pod evictions before repair. Defaults to `false`.
- `controller.uncordonAfterReboot`: uncordon controller-cordoned rebooted nodes after they return Ready. Defaults to `true`.
- `controller.deleteEmptyDirData`: allow draining pods that use `emptyDir` volumes. Defaults to `false`.
- `controller.dryRun`: logs repairs without changing cloud resources.
- `vultr.existingSecret`: secret containing the Vultr API key.
- `vultr.apiKeySecretKey`: secret key name. Defaults to `api-key`.
- `replicaCount`: keep at `1` until controller leader election is added.
