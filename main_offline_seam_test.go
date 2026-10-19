package main

import (
	"sort"
	"testing"

	webhooksvc "core/services/webhook"
)

// expectedGatewayPeriodicWorkers is the contract for the default (non-offline)
// runtime: every periodic chain-event, money-movement, and webhook-delivery loop
// stays supervisor-owned. Offline mode must register none of them, and adding a
// new periodic money/event worker must fail this test until it is listed here.
var expectedGatewayPeriodicWorkers = []string{
	"deposit-facts",
	"outbound-transactions",
	"provider-health",
	"reconciliation",
	"session-expiry",
	"sweep-jobs",
	"transaction-finality",
	"transfer-finalization",
	"webhook-retry",
}

func TestOfflineSupervisorRegistersNoPeriodicWorkers(t *testing.T) {
	supervisorInstance, err := buildGatewayWorkerSupervisor(webhooksvc.NewNotifier(), true)
	if err != nil {
		t.Fatalf("buildGatewayWorkerSupervisor(offline=true) error = %v", err)
	}
	if names := supervisorInstance.TaskNames(); len(names) != 0 {
		t.Fatalf("offline supervisor must register no periodic workers, got %v", names)
	}
}

func TestDefaultSupervisorRegistersEveryPeriodicWorker(t *testing.T) {
	supervisorInstance, err := buildGatewayWorkerSupervisor(webhooksvc.NewNotifier(), false)
	if err != nil {
		t.Fatalf("buildGatewayWorkerSupervisor(offline=false) error = %v", err)
	}
	if got, want := sortedNames(supervisorInstance.TaskNames()), expectedGatewayPeriodicWorkers; !equalStrings(got, want) {
		t.Fatalf("default supervisor tasks = %v, want %v", got, want)
	}
}

func TestGatewayPeriodicWorkerTasksMatchSupervisorContract(t *testing.T) {
	names := make([]string, 0, len(expectedGatewayPeriodicWorkers))
	for _, task := range gatewayPeriodicWorkerTasks(webhooksvc.NewNotifier()) {
		if task.Name == "" {
			t.Fatal("periodic worker task must have a name")
		}
		if task.Run == nil {
			t.Fatalf("periodic worker %q must have a run function", task.Name)
		}
		names = append(names, task.Name)
	}
	if got := sortedNames(names); !equalStrings(got, expectedGatewayPeriodicWorkers) {
		t.Fatalf("gatewayPeriodicWorkerTasks = %v, want %v", got, expectedGatewayPeriodicWorkers)
	}
}

func sortedNames(names []string) []string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return sorted
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
