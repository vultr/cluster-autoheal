package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vultr/cluster-autoheal/internal/cloudprovider"
	_ "github.com/vultr/cluster-autoheal/internal/cloudprovider/vultr"
	"github.com/vultr/cluster-autoheal/internal/config"
	"github.com/vultr/cluster-autoheal/internal/controller"
	"github.com/vultr/cluster-autoheal/internal/kube"
	"k8s.io/klog/v2"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	klog.InitFlags(nil)

	cfg := config.Default()
	flag.StringVar(&cfg.CloudProvider, "cloud-provider", cfg.CloudProvider, "Cloud provider implementation to use.")
	flag.StringVar(&cfg.Kubeconfig, "kubeconfig", cfg.Kubeconfig, "Path to kubeconfig. Empty uses in-cluster config.")
	flag.StringVar(&cfg.HealthAddr, "health-addr", cfg.HealthAddr, "Address for health and readiness endpoints.")
	flag.DurationVar(&cfg.ScanInterval, "scan-interval", cfg.ScanInterval, "How often to scan node health.")
	flag.DurationVar(&cfg.UnhealthyDuration, "unhealthy-duration", cfg.UnhealthyDuration, "How long a node must stay unhealthy before remediation.")
	flag.DurationVar(&cfg.DrainTimeout, "drain-timeout", cfg.DrainTimeout, "Maximum time to wait for pods to drain from a node.")
	flag.DurationVar(&cfg.RebootReadyTimeout, "reboot-ready-timeout", cfg.RebootReadyTimeout, "Maximum time to track a rebooted node for automatic uncordon.")
	flag.StringVar(&cfg.RepairAction, "repair-action", cfg.RepairAction, "Repair action to request from the cloud provider: reboot or replace.")
	flag.BoolVar(&cfg.CordonBeforeRepair, "cordon-before-repair", cfg.CordonBeforeRepair, "Cordon nodes before requesting repair.")
	flag.BoolVar(&cfg.DrainBeforeRepair, "drain-before-repair", cfg.DrainBeforeRepair, "Drain pods from nodes before requesting repair.")
	flag.BoolVar(&cfg.UncordonAfterReboot, "uncordon-after-reboot", cfg.UncordonAfterReboot, "Uncordon controller-cordoned nodes after reboot repair returns Ready.")
	flag.BoolVar(&cfg.DeleteEmptyDirData, "delete-emptydir-data", cfg.DeleteEmptyDirData, "Allow draining pods that use emptyDir volumes.")
	flag.BoolVar(&cfg.DryRun, "dry-run", cfg.DryRun, "Log repair decisions without changing cloud resources.")
	showVersion := flag.Bool("version", false, "Print version information and exit.")
	flag.Parse()
	if *showVersion {
		fmt.Printf("cluster-autoheal version=%s commit=%s date=%s\n", version, commit, date)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go runHealthServer(ctx, cfg.HealthAddr)

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

func runHealthServer(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "version=%s commit=%s date=%s\n", version, commit, date)
	})

	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	klog.Infof("starting health server on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		klog.Fatalf("health server stopped with error: %v", err)
	}
}
