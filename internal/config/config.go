package config

import "time"

const (
	ActionReboot  = "reboot"
	ActionReplace = "replace"
)

type Config struct {
	CloudProvider       string
	Kubeconfig          string
	HealthAddr          string
	ScanInterval        time.Duration
	UnhealthyDuration   time.Duration
	DrainTimeout        time.Duration
	RebootReadyTimeout  time.Duration
	RepairAction        string
	CordonBeforeRepair  bool
	DrainBeforeRepair   bool
	UncordonAfterReboot bool
	DeleteEmptyDirData  bool
	DryRun              bool
}

func Default() Config {
	return Config{
		CloudProvider:       "vultr",
		HealthAddr:          ":8080",
		ScanInterval:        30 * time.Second,
		UnhealthyDuration:   10 * time.Minute,
		DrainTimeout:        10 * time.Minute,
		RebootReadyTimeout:  15 * time.Minute,
		RepairAction:        ActionReboot,
		CordonBeforeRepair:  true,
		UncordonAfterReboot: true,
	}
}
