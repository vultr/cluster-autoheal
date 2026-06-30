package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadRepairRuleFileParsesDurations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repair-policy.yaml")
	data := []byte(`maxUnhealthyNodeThresholdPercentage: 10
maxParallelNodesRepairedCount: 2
rules:
  - condition: AcceleratedHardwareReady
    reason: NvidiaXID64Error
    minRepairWait: 5m
    action: replace
`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write repair policy: %v", err)
	}

	policy, err := LoadRepairRuleFile(path)
	if err != nil {
		t.Fatalf("LoadRepairRuleFile() error = %v", err)
	}
	if policy.MaxUnhealthyNodeThresholdPercentage != 10 {
		t.Fatalf("threshold = %d, want 10", policy.MaxUnhealthyNodeThresholdPercentage)
	}
	if policy.MaxParallelNodesRepairedCount != 2 {
		t.Fatalf("parallel count = %d, want 2", policy.MaxParallelNodesRepairedCount)
	}
	if len(policy.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(policy.Rules))
	}
	if policy.Rules[0].MinRepairWait.Duration != 5*time.Minute {
		t.Fatalf("min repair wait = %s, want 5m", policy.Rules[0].MinRepairWait.Duration)
	}
}
