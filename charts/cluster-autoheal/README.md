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
- `controller.actionOverrideLabel`: node label that overrides matched repair action.
- `controller.leaderElect`: enables Kubernetes leader election. Defaults to `true`.
- `controller.leaderElectionNamespace`: namespace for the leader election Lease.
- `controller.leaderElectionName`: name of the leader election Lease.
- `controller.cordonBeforeRepair`: cordon nodes before repair. Defaults to `true`.
- `controller.drainBeforeRepair`: drain nodes with pod evictions before repair. Defaults to `false`.
- `controller.uncordonAfterReboot`: uncordon controller-cordoned rebooted nodes after they return Ready. Defaults to `true`.
- `controller.deleteEmptyDirData`: allow draining pods that use `emptyDir` volumes. Defaults to `false`.
- `controller.dryRun`: logs repairs without changing cloud resources.
- `repairPolicy.rules`: condition/reason-specific repair rules with `minRepairWait` and `action`.
- `repairPolicy.maxUnhealthyNodeThresholdCount`: stop repairs above this candidate count.
- `repairPolicy.maxUnhealthyNodeThresholdPercentage`: stop repairs above this candidate percentage.
- `repairPolicy.maxParallelNodesRepairedCount`: maximum nodes repaired per scan.
- `repairPolicy.maxParallelNodesRepairedPercentage`: maximum percentage of candidates repaired per scan.
- `vultr.existingSecret`: secret containing the Vultr API key.
- `vultr.apiKeySecretKey`: secret key name. Defaults to `api-key`.
- `replicaCount`: number of controller pods. Defaults to `2` for leader election failover.
- `dnsPolicy`: pod DNS policy. Defaults to `Default` so cloud API calls do not depend on cluster DNS during repairs.
