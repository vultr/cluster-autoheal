package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/vultr/cluster-autoheal/internal/cloudprovider"
	_ "github.com/vultr/cluster-autoheal/internal/cloudprovider/vultr"
	"github.com/vultr/cluster-autoheal/internal/config"
	"github.com/vultr/cluster-autoheal/internal/controller"
	"github.com/vultr/cluster-autoheal/internal/kube"
	"k8s.io/klog/v2"
)

func main() {
	klog.InitFlags(nil)

	cfg := config.Default()
	flag.StringVar(&cfg.CloudProvider, "cloud-provider", cfg.CloudProvider, "Cloud provider implementation to use.")
	flag.StringVar(&cfg.Kubeconfig, "kubeconfig", cfg.Kubeconfig, "Path to kubeconfig. Empty uses in-cluster config.")
	flag.DurationVar(&cfg.ScanInterval, "scan-interval", cfg.ScanInterval, "How often to scan node health.")
	flag.DurationVar(&cfg.UnhealthyDuration, "unhealthy-duration", cfg.UnhealthyDuration, "How long a node must stay unhealthy before remediation.")
	flag.StringVar(&cfg.RepairAction, "repair-action", cfg.RepairAction, "Repair action to request from the cloud provider: reboot or replace.")
	flag.BoolVar(&cfg.DryRun, "dry-run", cfg.DryRun, "Log repair decisions without changing cloud resources.")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client, err := kube.NewClient(cfg.Kubeconfig)
	if err != nil {
		klog.Fatalf("failed to create kubernetes client: %v", err)
	}

	provider, err := cloudprovider.Build(cfg.CloudProvider)
	if err != nil {
		klog.Fatalf("failed to build cloud provider: %v", err)
	}

	c := controller.New(client, provider, cfg)
	if err := c.Run(ctx); err != nil {
		klog.Fatalf("controller stopped with error: %v", err)
	}
}
