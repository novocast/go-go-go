package race

import (
	"context"
	"testing"
	"time"
)

func TestRaceTrialsFinishAndProduceMedianSummary(t *testing.T) {
	definition, ok := FindDefinition("counter")
	if !ok {
		t.Fatal("counter race is missing")
	}
	definition.Work = 12_000
	definition.Build = func(_ int64, workers []int, work int) []Lane {
		return buildCounterLanes(0, workers, work)
	}
	events := make(chan Event, 4096)
	result, err := Run(context.Background(), definition, 7, []int{2}, 3, time.Second, events)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Trials) != 3 || len(result.Summary) != 4 {
		t.Fatalf("got %d trials and %d summaries", len(result.Trials), len(result.Summary))
	}
	if result.Summary[0].Elapsed <= 0 {
		t.Fatalf("median elapsed = %s", result.Summary[0].Elapsed)
	}
}

func TestSumStrategiesCompleteWithIdenticalInput(t *testing.T) {
	lanes := buildSumLanes(13, []int{2, 4}, 20_000)
	for _, lane := range lanes {
		elapsed, err := lane.Run(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: Run() error = %v", lane.Name(), err)
		}
		if elapsed <= 0 {
			t.Errorf("%s: elapsed = %s", lane.Name(), elapsed)
		}
	}
}

func TestSummaryUsesMedianAndRetiresRepeatedDNF(t *testing.T) {
	lanes := []Lane{&sumLane{name: "SEQ-1", workers: 1}}
	trials := []TrialResult{
		{Lanes: []LaneResult{{Index: 0, Name: "SEQ-1", Workers: 1, Elapsed: 10 * time.Millisecond}}},
		{Lanes: []LaneResult{{Index: 0, Name: "SEQ-1", Workers: 1, Elapsed: 20 * time.Millisecond, DNF: true}}},
		{Lanes: []LaneResult{{Index: 0, Name: "SEQ-1", Workers: 1, Elapsed: 30 * time.Millisecond, DNF: true}}},
	}
	summary := sortedSummary(trials, lanes)
	if summary[0].Elapsed != 20*time.Millisecond || !summary[0].DNF {
		t.Fatalf("summary = %#v, want median 20ms and DNF", summary[0])
	}
}
