package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gogogo/internal/broker"
	"gogogo/internal/commentary"
	"gogogo/internal/race"
	"gogogo/internal/track"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "gogogo:", err)
		os.Exit(1)
	}
}

func run() error {
	raceName := flag.String("race", "sum", "race to run: sum, counter, or all")
	trials := flag.Int("trials", 1, "timed trials per race (all defaults to 5)")
	maxTime := flag.Duration("max", 5*time.Second, "maximum time allowed for a lane")
	fps := flag.Int("fps", 30, "terminal refresh rate")
	noColor := flag.Bool("no-color", false, "disable ANSI lane colors")
	seed := flag.Int64("seed", 1, "seed for deterministic race input")
	workerList := flag.String("workers", "2,4,16,64", "comma-separated worker counts")
	procs := flag.Int("procs", runtime.GOMAXPROCS(0), "pin GOMAXPROCS to this value")
	flag.Parse()

	trialsProvided := false
	flag.Visit(func(item *flag.Flag) {
		if item.Name == "trials" {
			trialsProvided = true
		}
	})
	if *raceName == "all" && !trialsProvided {
		*trials = 5
	}
	if *trials < 1 || *maxTime <= 0 || *fps < 1 || *fps > 60 || *procs < 1 {
		return errors.New("trials, max, and procs must be positive; fps must be between 1 and 60")
	}
	workers, err := parseWorkers(*workerList)
	if err != nil {
		return err
	}
	runtime.GOMAXPROCS(*procs)

	var definitions []race.Definition
	switch *raceName {
	case "all":
		definitions = race.Definitions()
	case "sum", "counter":
		definition, ok := race.FindDefinition(*raceName)
		if !ok {
			return fmt.Errorf("unknown race %q", *raceName)
		}
		definitions = []race.Definition{definition}
	default:
		return fmt.Errorf("unknown race %q (choose sum, counter, or all)", *raceName)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	session, err := track.PrepareTerminal(os.Stdin, os.Stdout)
	if err != nil {
		return fmt.Errorf("prepare terminal: %w", err)
	}
	defer func() {
		if restoreErr := session.Restore(); restoreErr != nil {
			fmt.Fprintln(os.Stderr, "restore terminal:", restoreErr)
		}
	}()
	if err := session.Enter(); err != nil {
		return fmt.Errorf("enter alternate screen: %w", err)
	}
	keys := make(chan track.Key, 32)
	if session.RawInput() {
		go track.ReadKeys(os.Stdin, keys)
	} else {
		close(keys)
	}
	_, noColorEnvironment := os.LookupEnv("NO_COLOR")
	disableColor := *noColor || noColorEnvironment

	for index, definition := range definitions {
		eventInput := make(chan race.Event, 2048)
		renderEvents := make(chan race.Event, 512)
		commentaryEvents := make(chan race.Event, 512)
		commentaryLines := make(chan string, 16)
		go broker.FanOut(eventInput, renderEvents, commentaryEvents)
		go commentary.Run(commentaryEvents, commentaryLines)
		outcomes := make(chan race.Outcome, 1)
		go func(definition race.Definition) {
			result, runErr := race.Run(ctx, definition, *seed, workers, *trials, *maxTime, eventInput)
			close(eventInput)
			outcomes <- race.Outcome{Result: result, Err: runErr}
		}(definition)

		renderer := track.Renderer{
			Out:           os.Stdout,
			Session:       session,
			Keys:          keys,
			Cancel:        cancel,
			FPS:           *fps,
			NoColor:       disableColor,
			RaceNumber:    index + 1,
			RaceCount:     len(definitions),
			Definition:    definition,
			StartSequence: session.Interactive(),
		}
		if err := renderer.Run(ctx, renderEvents, commentaryLines, outcomes); err != nil {
			return err
		}
	}
	return nil
}

func parseWorkers(value string) ([]int, error) {
	parts := strings.Split(value, ",")
	workers := make([]int, 0, len(parts))
	seen := make(map[int]bool)
	for _, part := range parts {
		count, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || count < 1 {
			return nil, fmt.Errorf("invalid worker count %q", part)
		}
		if !seen[count] {
			workers = append(workers, count)
			seen[count] = true
		}
	}
	if len(workers) == 0 {
		return nil, errors.New("at least one worker count is required")
	}
	return workers, nil
}
