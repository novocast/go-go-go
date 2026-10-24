package race

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type EventKind uint8

const (
	EventRaceStarted EventKind = iota
	EventWarmupStarted
	EventWarmupComplete
	EventTrialStarted
	EventProgress
	EventFinished
	EventDNF
	EventRaceCompleted
)

type LaneInfo struct {
	Name    string
	Workers int
}

type Event struct {
	Kind      EventKind
	RaceName  string
	Trial     int
	Trials    int
	Lane      int
	LaneName  string
	Workers   int
	Fraction  float64
	Elapsed   time.Duration
	WarmupOK  bool
	Lanes     []LaneInfo
}

type Lane interface {
	Name() string
	Workers() int
	Run(context.Context, func(float64)) (time.Duration, error)
}

type Definition struct {
	Key   string
	Name  string
	Work  int
	Build func(seed int64, workers []int, work int) []Lane
}

type LaneResult struct {
	Index   int
	Name    string
	Workers int
	Elapsed time.Duration
	DNF     bool
}

type TrialResult struct {
	Number int
	Lanes  []LaneResult
}

type Result struct {
	Name    string
	Trials  []TrialResult
	Summary []LaneResult
	Cores   int
}

type Outcome struct {
	Result Result
	Err    error
}

func Definitions() []Definition {
	return []Definition{
		{Key: "sum", Name: "The Sum Sprint", Work: 50_000_000, Build: buildSumLanes},
		{Key: "counter", Name: "The Counter Clash", Work: 10_000_000, Build: buildCounterLanes},
	}
}

func FindDefinition(key string) (Definition, bool) {
	for _, definition := range Definitions() {
		if definition.Key == key {
			return definition, true
		}
	}
	return Definition{}, false
}

func (result Result) Note() string {
	if len(result.Summary) == 0 {
		return "No finishing times were recorded."
	}

	winner := result.Summary[0]
	var oversubscribed *LaneResult
	for index := range result.Summary {
		lane := &result.Summary[index]
		if lane.Workers > result.Cores && (oversubscribed == nil || lane.Workers > oversubscribed.Workers) {
			oversubscribed = lane
		}
	}
	if oversubscribed == nil {
		return "Every strategy ran within the pinned core count."
	}
	if oversubscribed.Name != winner.Name && oversubscribed.Elapsed > winner.Elapsed {
		return fmt.Sprintf("%d workers lost to %d; scheduling overhead exceeded the parallelism gain.", oversubscribed.Workers, winner.Workers)
	}
	return fmt.Sprintf("%d workers ran on %d cores; oversubscription can add scheduling overhead.", oversubscribed.Workers, result.Cores)
}

func sortedSummary(trials []TrialResult, lanes []Lane) []LaneResult {
	summary := make([]LaneResult, len(lanes))
	for index, lane := range lanes {
		elapsed := make([]time.Duration, 0, len(trials))
		dnfs := 0
		for _, trial := range trials {
			for _, result := range trial.Lanes {
				if result.Index == index {
					elapsed = append(elapsed, result.Elapsed)
					if result.DNF {
						dnfs++
					}
					break
				}
			}
		}
		sort.Slice(elapsed, func(i, j int) bool { return elapsed[i] < elapsed[j] })
		median := time.Duration(0)
		if len(elapsed) > 0 {
			middle := len(elapsed) / 2
			median = elapsed[middle]
			if len(elapsed)%2 == 0 {
				median = elapsed[middle-1]/2 + elapsed[middle]/2
			}
		}
		dnfThreshold := (len(trials) + 1) / 2
		summary[index] = LaneResult{
			Index:   index,
			Name:    lane.Name(),
			Workers: lane.Workers(),
			Elapsed: median,
			DNF:     dnfs >= dnfThreshold,
		}
	}
	sort.SliceStable(summary, func(i, j int) bool {
		if summary[i].DNF != summary[j].DNF {
			return !summary[i].DNF
		}
		return summary[i].Elapsed < summary[j].Elapsed
	})
	return summary
}