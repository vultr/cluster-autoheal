package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vultr/cluster-autoheal/internal/cloudprovider"
	_ "github.com/vultr/cluster-autoheal/internal/cloudprovider/vultr"
	"github.com/vultr/cluster-autoheal/internal/config"
	"github.com/vultr/cluster-autoheal/internal/controller"
	"github.com/vultr/cluster-autoheal/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
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
	flag.StringVar(&cfg.ConfigFile, "config-file", cfg.ConfigFile, "Path to repair policy YAML. Empty uses built-in defaults.")
	flag.StringVar(&cfg.Kubeconfig, "kubeconfig", cfg.Kubeconfig, "Path to kubeconfig. Empty uses in-cluster config.")
	flag.StringVar(&cfg.HealthAddr, "health-addr", cfg.HealthAddr, "Address for health and readiness endpoints.")
	flag.StringVar(&cfg.ActionOverrideLabel, "action-override-label", cfg.ActionOverrideLabel, "Node label that overrides matched repair action. Value must be reboot, replace, or none.")
	flag.BoolVar(&cfg.EnableLeaderElection, "leader-elect", cfg.EnableLeaderElection, "Use Kubernetes leader election before running repairs.")
	flag.StringVar(&cfg.LeaderElectionNamespace, "leader-election-namespace", cfg.LeaderElectionNamespace, "Namespace for the leader election Lease.")
	flag.StringVar(&cfg.LeaderElectionName, "leader-election-name", cfg.LeaderElectionName, "Name of the leader election Lease.")
	flag.DurationVar(&cfg.ScanInterval, "scan-interval", cfg.ScanInterval, "How often to scan node health.")
	flag.DurationVar(&cfg.DrainTimeout, "drain-timeout", cfg.DrainTimeout, "Maximum time to wait for pods to drain from a node.")
	flag.DurationVar(&cfg.RebootReadyTimeout, "reboot-ready-timeout", cfg.RebootReadyTimeout, "Maximum time to track a rebooted node for automatic uncordon.")
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
	if cfg.ConfigFile != "" {
		policy, err := config.LoadRepairRuleFile(cfg.ConfigFile)
		if err != nil {
			klog.Fatalf("failed to load repair policy: %v", err)
		}
		config.ApplyRepairRuleFile(&cfg, policy)
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

	run := func(runCtx context.Context) {
		c := controller.New(client, provider, cfg)
		if err := c.Run(runCtx); err != nil {
			klog.Fatalf("controller stopped with error: %v", err)
		}
	}
	if cfg.EnableLeaderElection {
		runWithLeaderElection(ctx, client, cfg, run)
		return
	}
	run(ctx)
}

func runWithLeaderElection(ctx context.Context, client kubernetes.Interface, cfg config.Config, run func(context.Context)) {
	identity := os.Getenv("POD_NAME")
	if identity == "" {
		hostname, err := os.Hostname()
		if err != nil {
			klog.Fatalf("failed to get hostname for leader election identity: %v", err)
		}
		identity = hostname
	}
	identity = strings.TrimSpace(identity)
	if identity == "" {
		klog.Fatal("leader election identity must not be empty")
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.LeaderElectionName,
			Namespace: cfg.LeaderElectionNamespace,
		},
		Client: client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: identity,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   30 * time.Second,
		RenewDeadline:   20 * time.Second,
		RetryPeriod:     5 * time.Second,
		Name:            cfg.LeaderElectionName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				klog.Infof("became leader as %s", identity)
				run(ctx)
			},
			OnStoppedLeading: func() {
				klog.Fatalf("leader election lost by %s", identity)
			},
			OnNewLeader: func(currentIdentity string) {
				if currentIdentity == identity {
					return
				}
				klog.Infof("new leader elected: %s", currentIdentity)
			},
		},
		WatchDog: leaderelection.NewLeaderHealthzAdaptor(20 * time.Second),
	})
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
