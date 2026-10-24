package race

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"time"
)

func Run(ctx context.Context, definition Definition, seed int64, workers []int, trials int, maxTime time.Duration, events chan<- Event) (Result, error) {
	if definition.Build == nil || definition.Work <= 0 {
		return Result{}, errors.New("race definition is incomplete")
	}
	if trials < 1 || maxTime <= 0 {
		return Result{}, errors.New("trials and max time must be positive")
	}
	lanes := definition.Build(seed, workers, definition.Work)
	if len(lanes) == 0 {
		return Result{}, errors.New("race has no lanes")
	}
	laneInfo := make([]LaneInfo, len(lanes))
	for index, lane := range lanes {
		laneInfo[index] = LaneInfo{Name: lane.Name(), Workers: lane.Workers()}
	}
	if err := sendEvent(ctx, events, Event{Kind: EventRaceStarted, RaceName: definition.Name, Trials: trials, Lanes: laneInfo}); err != nil {
		return Result{}, err
	}
	if err := sendEvent(ctx, events, Event{Kind: EventWarmupStarted, RaceName: definition.Name}); err != nil {
		return Result{}, err
	}
	warmupWork := max(1, definition.Work/10)
	warmupLanes := definition.Build(seed, workers, warmupWork)
	warmup, err := runTrial(ctx, warmupLanes, 0, trials, maxTime, events, false)
	if err != nil {
		return Result{}, err
	}
	warmupOK := true
	for _, lane := range warmup {
		warmupOK = warmupOK && !lane.DNF
	}
	if err := sendEvent(ctx, events, Event{Kind: EventWarmupComplete, RaceName: definition.Name, WarmupOK: warmupOK}); err != nil {
		return Result{}, err
	}

	result := Result{Name: definition.Name, Cores: runtime.GOMAXPROCS(0)}
	for trial := 1; trial <= trials; trial++ {
		laneResults, err := runTrial(ctx, lanes, trial, trials, maxTime, events, true)
		if err != nil {
			return result, err
		}
		result.Trials = append(result.Trials, TrialResult{Number: trial, Lanes: laneResults})
	}
	result.Summary = sortedSummary(result.Trials, lanes)
	if err := sendEvent(ctx, events, Event{Kind: EventRaceCompleted, RaceName: definition.Name, Trials: trials}); err != nil {
		return result, err
	}
	return result, nil
}

func runTrial(ctx context.Context, lanes []Lane, trial, trials int, maxTime time.Duration, events chan<- Event, emit bool) ([]LaneResult, error) {
	if emit {
		laneInfo := make([]LaneInfo, len(lanes))
		for index, lane := range lanes {
			laneInfo[index] = LaneInfo{Name: lane.Name(), Workers: lane.Workers()}
		}
		if err := sendEvent(ctx, events, Event{Kind: EventTrialStarted, Trial: trial, Trials: trials, Lanes: laneInfo}); err != nil {
			return nil, err
		}
	}
	start := make(chan struct{})
	results := make(chan LaneResult, len(lanes))
	started := time.Now()
	for index, lane := range lanes {
		go func(index int, lane Lane) {
			<-start
			laneContext, cancel := context.WithTimeout(ctx, maxTime)
			var elapsed time.Duration
			var runErr error
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						runErr = fmt.Errorf("lane panic: %v", recovered)
					}
				}()
				var progress func(float64)
				if emit {
					progress = func(fraction float64) {
						if ctx.Err() != nil {
							return
						}
						event := Event{
							Kind: EventProgress, Trial: trial, Trials: trials, Lane: index,
							LaneName: lane.Name(), Workers: lane.Workers(), Fraction: fraction,
						}
						select {
						case events <- event:
						default:
						}
					}
				}
				elapsed, runErr = lane.Run(laneContext, progress)
			}()
			cancel()
			elapsed = elapsedSince(started)
			laneResult := LaneResult{Index: index, Name: lane.Name(), Workers: lane.Workers(), Elapsed: elapsed}
			if runErr != nil && ctx.Err() == nil {
				laneResult.DNF = true
			}
			if emit && ctx.Err() == nil {
				kind := EventFinished
				if laneResult.DNF {
					kind = EventDNF
				}
				sendEvent(ctx, events, Event{
					Kind: kind, Trial: trial, Trials: trials, Lane: index,
					LaneName: lane.Name(), Workers: lane.Workers(), Elapsed: elapsed,
				})
			}
			results <- laneResult
		}(index, lane)
	}
	close(start)
	laneResults := make([]LaneResult, 0, len(lanes))
	for range lanes {
		laneResults = append(laneResults, <-results)
	}
	if err := ctx.Err(); err != nil {
		return laneResults, err
	}
	sort.SliceStable(laneResults, func(i, j int) bool { return laneResults[i].Elapsed < laneResults[j].Elapsed })
	return laneResults, nil
}

func elapsedSince(started time.Time) time.Duration {
	elapsed := time.Since(started)
	if elapsed <= 0 {
		return time.Nanosecond
	}
	return elapsed
}

func sendEvent(ctx context.Context, events chan<- Event, event Event) error {
	select {
	case events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
