package race

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

type sumLane struct {
	name     string
	workers  int
	data     []uint32
	expected uint64
}

func (lane *sumLane) Name() string   { return lane.name }
func (lane *sumLane) Workers() int   { return lane.workers }
func (lane *sumLane) Run(ctx context.Context, progress func(float64)) (time.Duration, error) {
	started := time.Now()
	meter := newProgressMeter(int64(len(lane.data)), progress)
	var total uint64
	if lane.workers == 1 {
		chunk := chunkSize(len(lane.data), 1)
		for start := 0; start < len(lane.data); start += chunk {
			if err := ctx.Err(); err != nil {
				return elapsedSince(started), err
			}
			end := min(start+chunk, len(lane.data))
			for _, value := range lane.data[start:end] {
				total += uint64(value)
			}
			meter.advance(int64(end - start))
		}
	} else {
		partials := make([]uint64, lane.workers)
		errorsByWorker := make([]error, lane.workers)
		var group sync.WaitGroup
		chunk := chunkSize(len(lane.data), lane.workers)
		for worker := 0; worker < lane.workers; worker++ {
			start, end := partition(len(lane.data), lane.workers, worker)
			group.Add(1)
			go func(worker, start, end int) {
				defer group.Done()
				for position := start; position < end; position += chunk {
					if err := ctx.Err(); err != nil {
						errorsByWorker[worker] = err
						return
					}
					stop := min(position+chunk, end)
					for _, value := range lane.data[position:stop] {
						partials[worker] += uint64(value)
					}
					meter.advance(int64(stop - position))
				}
			}(worker, start, end)
		}
		group.Wait()
		for _, err := range errorsByWorker {
			if err != nil {
				return elapsedSince(started), err
			}
		}
		for _, partial := range partials {
			total += partial
		}
	}
	if total != lane.expected {
		return elapsedSince(started), fmt.Errorf("sum mismatch: got %d, want %d", total, lane.expected)
	}
	return elapsedSince(started), nil
}

type counterStrategy uint8

const (
	strategyMutex counterStrategy = iota
	strategyAtomic
	strategyChannel
	strategySharded
)

type counterLane struct {
	name     string
	workers  int
	work     int
	strategy counterStrategy
}

func (lane *counterLane) Name() string { return lane.name }
func (lane *counterLane) Workers() int { return lane.workers }
func (lane *counterLane) Run(ctx context.Context, progress func(float64)) (time.Duration, error) {
	started := time.Now()
	meter := newProgressMeter(int64(lane.work), progress)
	var count int64
	var err error
	switch lane.strategy {
	case strategyMutex:
		var mutex sync.Mutex
		err = runCounterWorkers(ctx, lane.work, lane.workers, func(_ int, start, end int) error {
			for position := start; position < end; {
				if err := ctx.Err(); err != nil {
					return err
				}
				stop := min(position+chunkSize(lane.work, lane.workers), end)
				for range stop - position {
					mutex.Lock()
					count++
					mutex.Unlock()
				}
				meter.advance(int64(stop - position))
				position = stop
			}
			return nil
		})
	case strategyAtomic:
		var counter atomic.Int64
		err = runCounterWorkers(ctx, lane.work, lane.workers, func(_ int, start, end int) error {
			for position := start; position < end; {
				if err := ctx.Err(); err != nil {
					return err
				}
				stop := min(position+chunkSize(lane.work, lane.workers), end)
				for range stop - position {
					counter.Add(1)
				}
				meter.advance(int64(stop - position))
				position = stop
			}
			return nil
		})
		count = counter.Load()
	case strategyChannel:
		type batch struct{ size int }
		batches := make(chan batch, lane.workers*2)
		var senders sync.WaitGroup
		for worker := 0; worker < lane.workers; worker++ {
			start, end := partition(lane.work, lane.workers, worker)
			senders.Add(1)
			go func(start, end int) {
				defer senders.Done()
				for position := start; position < end; {
					if ctx.Err() != nil {
						return
					}
					amount := min(1024, end-position)
					select {
					case batches <- batch{size: amount}:
						position += amount
					case <-ctx.Done():
						return
					}
				}
			}(start, end)
		}
		go func() {
			senders.Wait()
			close(batches)
		}()
		for next := range batches {
			for range next.size {
				count++
			}
			meter.advance(int64(next.size))
		}
		err = ctx.Err()
	case strategySharded:
		shards := make([]atomic.Int64, lane.workers)
		err = runCounterWorkers(ctx, lane.work, lane.workers, func(worker, start, end int) error {
			for position := start; position < end; {
				if err := ctx.Err(); err != nil {
					return err
				}
				stop := min(position+chunkSize(lane.work, lane.workers), end)
				for range stop - position {
					shards[worker].Add(1)
				}
				meter.advance(int64(stop - position))
				position = stop
			}
			return nil
		})
		for index := range shards {
			count += shards[index].Load()
		}
	}
	if err != nil {
		return elapsedSince(started), err
	}
	if count != int64(lane.work) {
		return elapsedSince(started), fmt.Errorf("counter mismatch: got %d, want %d", count, lane.work)
	}
	return elapsedSince(started), nil
}

type progressMeter struct {
	total     int64
	threshold int64
	completed atomic.Int64
	next      atomic.Int64
	report    func(float64)
}

func newProgressMeter(total int64, report func(float64)) *progressMeter {
	threshold := max(int64(1), total/100)
	meter := &progressMeter{total: total, threshold: threshold, report: report}
	meter.next.Store(threshold)
	return meter
}

func (meter *progressMeter) advance(amount int64) {
	completed := meter.completed.Add(amount)
	if meter.report == nil {
		return
	}
	for next := meter.next.Load(); completed >= next; next = meter.next.Load() {
		if meter.next.CompareAndSwap(next, next+meter.threshold) {
			fraction := min(float64(completed)/float64(meter.total), 1)
			meter.report(fraction)
			return
		}
	}
}

func buildSumLanes(seed int64, workers []int, work int) []Lane {
	random := rand.New(rand.NewSource(seed))
	data := make([]uint32, work)
	var expected uint64
	for index := range data {
		data[index] = uint32(random.Intn(100) + 1)
		expected += uint64(data[index])
	}
	lanes := []Lane{&sumLane{name: "SEQ-1", workers: 1, data: data, expected: expected}}
	for _, count := range workers {
		lanes = append(lanes, &sumLane{
			name:     fmt.Sprintf("%d-WRK", count),
			workers:  count,
			data:     data,
			expected: expected,
		})
	}
	return lanes
}

func buildCounterLanes(_ int64, workers []int, work int) []Lane {
	workerCount := 1
	for _, count := range workers {
		workerCount = max(workerCount, count)
	}
	strategies := []struct {
		name     string
		strategy counterStrategy
	}{
		{name: "MUTEX", strategy: strategyMutex},
		{name: "ATOMIC", strategy: strategyAtomic},
		{name: "CHANNEL", strategy: strategyChannel},
		{name: "SHARDED", strategy: strategySharded},
	}
	lanes := make([]Lane, 0, len(strategies))
	for _, item := range strategies {
		lanes = append(lanes, &counterLane{
			name:     fmt.Sprintf("%s-%d", item.name, workerCount),
			workers:  workerCount,
			work:     work,
			strategy: item.strategy,
		})
	}
	return lanes
}

func runCounterWorkers(ctx context.Context, total, workers int, work func(worker, start, end int) error) error {
	errorsByWorker := make([]error, workers)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		start, end := partition(total, workers, worker)
		group.Add(1)
		go func(worker, start, end int) {
			defer group.Done()
			errorsByWorker[worker] = work(worker, start, end)
		}(worker, start, end)
	}
	group.Wait()
	for _, err := range errorsByWorker {
		if err != nil {
			return err
		}
	}
	return nil
}

func partition(total, workers, worker int) (int, int) {
	base, extra := total/workers, total%workers
	start := worker*base + min(worker, extra)
	length := base
	if worker < extra {
		length++
	}
	return start, start + length
}

func chunkSize(total, workers int) int {
	return max(1, total/max(1, workers*100))
}