package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	ActionReboot   = "reboot"
	ActionReplace  = "replace"
	ActionNoAction = "none"
)

type RepairRule struct {
	Condition     string   `json:"condition"`
	Reason        string   `json:"reason,omitempty"`
	MinRepairWait Duration `json:"minRepairWait"`
	Action        string   `json:"action"`
}

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		d.Duration = duration
		return nil
	}

	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return err
	}
	d.Duration = time.Duration(nanos)
	return nil
}

type RepairRuleFile struct {
	MaxUnhealthyNodeThresholdCount      int          `json:"maxUnhealthyNodeThresholdCount,omitempty"`
	MaxUnhealthyNodeThresholdPercentage int          `json:"maxUnhealthyNodeThresholdPercentage,omitempty"`
	MaxParallelNodesRepairedCount       int          `json:"maxParallelNodesRepairedCount,omitempty"`
	MaxParallelNodesRepairedPercentage  int          `json:"maxParallelNodesRepairedPercentage,omitempty"`
	Rules                               []RepairRule `json:"rules,omitempty"`
}

type Config struct {
	CloudProvider                       string
	ConfigFile                          string
	Kubeconfig                          string
	HealthAddr                          string
	ActionOverrideLabel                 string
	LeaderElectionNamespace             string
	LeaderElectionName                  string
	EnableLeaderElection                bool
	ScanInterval                        time.Duration
	DrainTimeout                        time.Duration
	RebootReadyTimeout                  time.Duration
	RepairRules                         []RepairRule
	MaxUnhealthyNodeThresholdCount      int
	MaxUnhealthyNodeThresholdPercentage int
	MaxParallelNodesRepairedCount       int
	MaxParallelNodesRepairedPercentage  int
	CordonBeforeRepair                  bool
	DrainBeforeRepair                   bool
	UncordonAfterReboot                 bool
	DeleteEmptyDirData                  bool
	DryRun                              bool
}

func Default() Config {
	return Config{
		CloudProvider:                       "vultr",
		HealthAddr:                          ":8080",
		ActionOverrideLabel:                 "cluster-autoheal.vultr.com/repair-action",
		LeaderElectionNamespace:             "kube-system",
		LeaderElectionName:                  "cluster-autoheal",
		EnableLeaderElection:                true,
		ScanInterval:                        30 * time.Second,
		DrainTimeout:                        10 * time.Minute,
		RebootReadyTimeout:                  15 * time.Minute,
		RepairRules:                         DefaultRepairRules(),
		MaxUnhealthyNodeThresholdPercentage: 20,
		MaxParallelNodesRepairedCount:       1,
		CordonBeforeRepair:                  true,
		UncordonAfterReboot:                 true,
	}
}

func DefaultRepairRules() []RepairRule {
	return []RepairRule{
		{Condition: "AcceleratedHardwareReady", MinRepairWait: Duration{Duration: 10 * time.Minute}, Action: ActionReboot},
		{Condition: "ContainerRuntimeReady", MinRepairWait: Duration{Duration: 30 * time.Minute}, Action: ActionReplace},
		{Condition: "KernelReady", MinRepairWait: Duration{Duration: 30 * time.Minute}, Action: ActionReplace},
		{Condition: "NetworkingReady", MinRepairWait: Duration{Duration: 30 * time.Minute}, Action: ActionReplace},
		{Condition: "StorageReady", MinRepairWait: Duration{Duration: 30 * time.Minute}, Action: ActionReplace},
		{Condition: "Ready", MinRepairWait: Duration{Duration: 30 * time.Minute}, Action: ActionReplace},
	}
}

func LoadRepairRuleFile(path string) (RepairRuleFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RepairRuleFile{}, err
	}

	var file RepairRuleFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return RepairRuleFile{}, fmt.Errorf("parse repair policy %s: %w", path, err)
	}

	return file, nil
}

func ApplyRepairRuleFile(cfg *Config, file RepairRuleFile) {
	if file.MaxUnhealthyNodeThresholdCount > 0 {
		cfg.MaxUnhealthyNodeThresholdCount = file.MaxUnhealthyNodeThresholdCount
		cfg.MaxUnhealthyNodeThresholdPercentage = 0
	}
	if file.MaxUnhealthyNodeThresholdPercentage > 0 {
		cfg.MaxUnhealthyNodeThresholdPercentage = file.MaxUnhealthyNodeThresholdPercentage
		cfg.MaxUnhealthyNodeThresholdCount = 0
	}
	if file.MaxParallelNodesRepairedCount > 0 {
		cfg.MaxParallelNodesRepairedCount = file.MaxParallelNodesRepairedCount
		cfg.MaxParallelNodesRepairedPercentage = 0
	}
	if file.MaxParallelNodesRepairedPercentage > 0 {
		cfg.MaxParallelNodesRepairedPercentage = file.MaxParallelNodesRepairedPercentage
		cfg.MaxParallelNodesRepairedCount = 0
	}
	if len(file.Rules) > 0 {
		cfg.RepairRules = file.Rules
	}
}
