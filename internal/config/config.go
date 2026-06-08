package config

import "time"

const (
	ActionReboot  = "reboot"
	ActionReplace = "replace"
)

type Config struct {
	CloudProvider     string
	Kubeconfig        string
	ScanInterval      time.Duration
	UnhealthyDuration time.Duration
	RepairAction      string
	DryRun            bool
}

func Default() Config {
	return Config{
		CloudProvider:     "vultr",
		ScanInterval:      30 * time.Second,
		UnhealthyDuration: 10 * time.Minute,
		RepairAction:      ActionReboot,
	}
}
