package db

import (
	"errors"
	"testing"
	"time"

	"github.com/Lakr233/minis/server/internal/model"
)

func TestSelectWorkstationCandidate_PrefersLeastLoadedWorker(t *testing.T) {
	workers := []model.Worker{
		{ID: "worker-b", PoolSize: 2},
		{ID: "worker-a", PoolSize: 2},
	}
	existing := map[string]map[int]bool{
		"worker-b": {0: true},
	}

	candidate, err := selectWorkstationCandidate(workers, existing, "")
	if err != nil {
		t.Fatalf("selectWorkstationCandidate returned error: %v", err)
	}
	if candidate.workerID != "worker-a" {
		t.Fatalf("workerID = %q, want %q", candidate.workerID, "worker-a")
	}
	if candidate.slot != 0 {
		t.Fatalf("slot = %d, want %d", candidate.slot, 0)
	}
}

func TestSelectWorkstationCandidate_UsesRequestedWorkerWhenAvailable(t *testing.T) {
	workers := []model.Worker{
		{ID: "worker-a", PoolSize: 2},
		{ID: "worker-b", PoolSize: 2},
	}
	existing := map[string]map[int]bool{
		"worker-b": {0: true},
	}

	candidate, err := selectWorkstationCandidate(workers, existing, "worker-b")
	if err != nil {
		t.Fatalf("selectWorkstationCandidate returned error: %v", err)
	}
	if candidate.workerID != "worker-b" {
		t.Fatalf("workerID = %q, want %q", candidate.workerID, "worker-b")
	}
	if candidate.slot != 1 {
		t.Fatalf("slot = %d, want %d", candidate.slot, 1)
	}
}

func TestSelectWorkstationCandidate_RejectsUnavailableRequestedWorker(t *testing.T) {
	workers := []model.Worker{
		{ID: "worker-a", PoolSize: 2, LastHeartbeat: ptrTime(time.Now())},
	}

	_, err := selectWorkstationCandidate(workers, map[string]map[int]bool{}, "worker-missing")
	if !errors.Is(err, ErrTargetWorkerUnavailable) {
		t.Fatalf("err = %v, want ErrTargetWorkerUnavailable", err)
	}
}

func TestSelectWorkstationCandidate_RejectsFullRequestedWorker(t *testing.T) {
	workers := []model.Worker{
		{ID: "worker-a", PoolSize: 2},
	}
	existing := map[string]map[int]bool{
		"worker-a": {0: true, 1: true},
	}

	_, err := selectWorkstationCandidate(workers, existing, "worker-a")
	if !errors.Is(err, ErrTargetWorkerFull) {
		t.Fatalf("err = %v, want ErrTargetWorkerFull", err)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
