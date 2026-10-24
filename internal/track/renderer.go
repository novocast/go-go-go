package track

import (
	"context"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gogogo/internal/race"
)

const (
	minimumWidth          = 72
	minimumHeight         = 15
	replayFactor          = 20
	minimumReplayDuration = 8 * time.Second
	maximumReplayDuration = 30 * time.Second
	startLightInterval    = 400 * time.Millisecond
	startLightsHold       = 500 * time.Millisecond
	startGoHold           = 800 * time.Millisecond
	startGoFlashInterval  = 500 * time.Millisecond
	startSequenceRows     = 5
	startLightsDuration   = 5*startLightInterval + startLightsHold
	startSequenceDuration = startLightsDuration + startGoHold
	replayGoTarget        = 0.5
)

var laneColors = []int{39, 208, 46, 201, 226, 51, 196, 141}
var goGoGoArt = []string{
	"▄──▄ ▄──▄     ▄──▄ ▄──▄     ▄──▄ ▄──▄ ▄     ",
	"█ ─▄ █  █     █ ─▄ █  █     █ ─▄ █  █ █     ",
	"█  █ █  █     █  █ █  █     █  █ █  █ ▀     ",
	"▀──▀ ▀──▀     ▀──▀ ▀──▀     ▀──▀ ▀──▀ ▀     ",
}

type Renderer struct {
	Out           io.Writer
	Session       *TerminalSession
	Keys          <-chan Key
	Cancel        context.CancelFunc
	StartSequence bool
	FPS           int
	NoColor       bool
	RaceNumber    int
	RaceCount     int
	Definition    race.Definition
}

type laneState struct {
	name     string
	workers  int
	progress float64
	elapsed  time.Duration
	finished bool
	dnf      bool
}

type screenState struct {
	raceName              string
	trial                 int
	trials                int
	warmupDone            bool
	warmupOK              bool
	lanes                 []laneState
	selected              int
	started               time.Time
	lastFinished          int
	comment               string
	frame                 int
	checkered             bool
	startSequenceStarted  time.Time
	startSequenceComplete bool
	startReplayGoStarted  time.Time
	startProgressReached  bool
	small                 bool
	result                race.Result
	resultReady           bool
	currentNumber         int
	raceCount             int
	replay                bool
}

func (renderer Renderer) Run(ctx context.Context, events <-chan race.Event, commentary <-chan string, outcomes <-chan race.Outcome) error {
	if renderer.FPS < 1 {
		renderer.FPS = 30
	}
	if renderer.Session.Interactive() {
		if err := renderer.Session.Enter(); err != nil {
			return err
		}
	}
	state := screenState{
		raceName:      renderer.Definition.Name,
		currentNumber: renderer.RaceNumber,
		raceCount:     renderer.RaceCount,
		lastFinished:  -1,
		comment:       "And it's Go, go, go!",
	}
	if !renderer.Session.Interactive() {
		return renderer.runPlain(ctx, &state, events, commentary, outcomes)
	}

	keys := renderer.Keys
	if !renderer.Session.RawInput() {
		keys = nil
	}
	ticker := time.NewTicker(time.Second / time.Duration(renderer.FPS))
	defer ticker.Stop()
	var outcomeChannel <-chan race.Outcome = outcomes
	var outcome race.Outcome
	phase := 0
	var phaseStarted time.Time
	var replayLength time.Duration
	for {
		if events == nil && commentary == nil && outcomeChannel == nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if phase == 0 && outcome.Err != nil {
				phaseStarted = time.Now()
				state.comment = outcome.Err.Error()
				state.checkered = true
				phase = 2
			}
			if phase == 0 && outcome.Err == nil && (!renderer.StartSequence || state.startSequenceComplete) {
				phaseStarted = time.Now()
				state.result = outcome.Result
				state.resultReady = true
				replayLength = replayDuration(state.result)
				prepareReplay(&state, state.result)
				state.startReplayGoStarted = phaseStarted
				phase = 1
			}
			if phase == 1 {
				replayElapsed := time.Since(phaseStarted)
				advanceReplay(&state, replayElapsed, replayLength)
				if replayElapsed >= replayLength {
					state.checkered = true
					phase = 2
					phaseStarted = time.Now()
				}
			}
			if phase == 2 && !renderer.Session.RawInput() && time.Since(phaseStarted) >= 3*time.Second {
				return outcome.Err
			}
		}

		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			state.apply(event)
			if event.Kind == race.EventTrialStarted && renderer.StartSequence && state.startSequenceStarted.IsZero() {
				state.startSequenceStarted = time.Now()
			}
		case line, ok := <-commentary:
			if !ok {
				commentary = nil
				continue
			}
			state.comment = line
		case result, ok := <-outcomeChannel:
			if !ok {
				outcomeChannel = nil
				continue
			}
			outcome = result
			outcomeChannel = nil
		case key, ok := <-keys:
			if !ok {
				keys = nil
				continue
			}
			if phase == 2 {
				return outcome.Err
			}
			switch key {
			case KeyUp:
				if len(state.lanes) > 0 {
					state.selected = (state.selected - 1 + len(state.lanes)) % len(state.lanes)
				}
			case KeyDown:
				if len(state.lanes) > 0 {
					state.selected = (state.selected + 1) % len(state.lanes)
				}
			case KeyInterrupt, KeyQuit:
				renderer.Cancel()
			}
		case <-ticker.C:
			width, height := renderer.Session.Size()
			if width < minimumWidth || height < minimumHeight {
				if !state.small {
					io.WriteString(renderer.Out, "\x1b[2J")
					state.small = true
				}
				if err := renderer.writeSmall(width, height); err != nil {
					return err
				}
				continue
			}
			if state.small {
				io.WriteString(renderer.Out, "\x1b[2J")
				state.small = false
			}
			state.frame++
			if phase == 2 {
				if err := renderer.writeResult(&state, width); err != nil {
					return err
				}
			} else {
				if err := renderer.writeLive(&state, width, height); err != nil {
					return err
				}
				if renderer.StartSequence && !state.startSequenceStarted.IsZero() && !state.startSequenceComplete {
					_, ready := renderer.startSequenceLines(time.Since(state.startSequenceStarted))
					if ready {
						state.startSequenceComplete = true
					}
				}
			}
		}
	}
}

func (renderer Renderer) runPlain(ctx context.Context, state *screenState, events <-chan race.Event, commentary <-chan string, outcomes <-chan race.Outcome) error {
	contextDone := ctx.Done()
	for events != nil || commentary != nil || outcomes != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			state.apply(event)
		case line, ok := <-commentary:
			if !ok {
				commentary = nil
				continue
			}
			state.comment = line
		case outcome, ok := <-outcomes:
			if !ok {
				outcomes = nil
				continue
			}
			if outcome.Err != nil {
				return outcome.Err
			}
			state.result = outcome.Result
			state.resultReady = true
			outcomes = nil
		case <-contextDone:
			contextDone = nil
			if outcomes == nil && events == nil && commentary == nil {
				return ctx.Err()
			}
		}
	}
	if !state.resultReady {
		return ctx.Err()
	}
	return renderer.writePlainResult(state)
}

func (state *screenState) apply(event race.Event) {
	switch event.Kind {
	case race.EventRaceStarted:
		state.raceName = event.RaceName
		state.trials = event.Trials
		state.selected = 0
		state.lanes = make([]laneState, len(event.Lanes))
		state.startProgressReached = false
		for index, lane := range event.Lanes {
			state.lanes[index] = laneState{name: lane.Name, workers: lane.Workers}
		}
	case race.EventWarmupStarted:
		state.warmupDone = false
	case race.EventWarmupComplete:
		state.warmupDone = true
		state.warmupOK = event.WarmupOK
	case race.EventTrialStarted:
		state.trial = event.Trial
		state.trials = event.Trials
		state.started = time.Now()
		state.lastFinished = -1
		for index := range state.lanes {
			state.lanes[index].progress = 0
			state.lanes[index].elapsed = 0
			state.lanes[index].finished = false
			state.lanes[index].dnf = false
		}
	case race.EventProgress:
		if event.Lane >= 0 && event.Lane < len(state.lanes) {
			state.lanes[event.Lane].progress = math.Max(state.lanes[event.Lane].progress, event.Fraction)
		}
	case race.EventFinished:
		if event.Lane >= 0 && event.Lane < len(state.lanes) {
			lane := &state.lanes[event.Lane]
			lane.finished = true
			lane.elapsed = event.Elapsed
			lane.progress = 1
			state.lastFinished = event.Lane
		}
	case race.EventDNF:
		if event.Lane >= 0 && event.Lane < len(state.lanes) {
			lane := &state.lanes[event.Lane]
			lane.finished = true
			lane.dnf = true
			lane.elapsed = event.Elapsed
			state.lastFinished = event.Lane
		}
	}
}

func (renderer Renderer) writeLive(state *screenState, width, height int) error {
	inner := width - 4
	trial := state.trial
	if trial == 0 {
		trial = 1
	}
	title := fmt.Sprintf("GoGoGo Grand Prix | Race %d/%d: %s", state.currentNumber, state.raceCount, state.raceName)
	warmup := "warmup ..."
	if state.warmupDone && state.warmupOK {
		warmup = "warmup ✓"
	} else if state.warmupDone {
		warmup = "warmup DNF"
	}
	subtitle := fmt.Sprintf("trial %d/%d   %s   UP/DOWN lanes %d/%d   GOMAXPROCS=%d", trial, max(1, state.trials), warmup, min(state.selected+1, max(1, len(state.lanes))), len(state.lanes), raceCores())
	if state.replay {
		subtitle = fmt.Sprintf("FINAL LAP   MEDIAN-TIME REPLAY   GOMAXPROCS=%d", raceCores())
	}
	countdownOnly := renderer.StartSequence && !state.replay
	var startLines []string
	if renderer.StartSequence && !state.startSequenceStarted.IsZero() {
		startLines = make([]string, startSequenceRows)
		if !state.startSequenceComplete {
			startLines, _ = renderer.startSequenceLines(time.Since(state.startSequenceStarted))
		} else if state.replay && !state.startProgressReached {
			startLines = renderer.flashingGoGoGoLines(time.Since(state.startReplayGoStarted))
		}
	}
	maxRows := max(1, height-9-len(startLines))
	start := 0
	if len(state.lanes) > maxRows && state.selected >= maxRows {
		start = state.selected - maxRows + 1
	}
	lines := []string{border(width, '┌', '─', '┐'), boxLine(title, inner), boxLine(subtitle, inner), border(width, '├', '─', '┤')}
	for _, line := range startLines {
		padding := max(0, (inner-visibleWidth(line))/2)
		lines = append(lines, boxLine(strings.Repeat(" ", padding)+line, inner))
	}
	end := min(len(state.lanes), start+maxRows)
	for index := start; index < end; index++ {
		if countdownOnly {
			lines = append(lines, boxLine("", inner))
		} else {
			lines = append(lines, boxLine(renderer.laneLine(state, index, inner), inner))
		}
	}
	lines = append(lines, border(width, '├', '─', '┤'))
	if countdownOnly {
		lines = append(lines, boxLine("", inner), boxLine("", inner))
	} else {
		lines = append(lines, boxLine(renderer.statusLine(state), inner), boxLine(state.comment, inner))
	}
	lines = append(lines, boxLine("CTRL-C quits | finish order is measured time, not car position", inner))
	lines = append(lines, border(width, '└', '─', '┘'))
	return renderer.writeFrame(lines, width)
}

func (renderer Renderer) startSequenceLines(elapsed time.Duration) ([]string, bool) {
	if elapsed < startLightsDuration {
		lit := min(5, int(elapsed/startLightInterval)+1)
		return renderer.startLightLines(lit), false
	}
	return renderer.goGoGoLines(), elapsed >= startSequenceDuration
}

func (renderer Renderer) goGoGoLines() []string {
	lines := make([]string, startSequenceRows)
	copy(lines, goGoGoArt)
	for index := range goGoGoArt {
		if !renderer.NoColor {
			lines[index] = "\x1b[1;32m" + lines[index] + "\x1b[0m"
		}
	}
	return lines
}

func (renderer Renderer) flashingGoGoGoLines(elapsed time.Duration) []string {
	if (elapsed/startGoFlashInterval)%2 == 1 {
		return make([]string, startSequenceRows)
	}
	return renderer.goGoGoLines()
}

func (renderer Renderer) startLightLines(lit int) []string {
	var lines [5]string
	for light := 0; light < 5; light++ {
		art := [5]string{" .---. ", "/     \\", "|     |", "\\     /", " '---' "}
		if light < lit {
			art = [5]string{" .---. ", "/#####\\", "|#####|", "\\#####/", " '---' "}
		}
		for row, segment := range art {
			if light < lit && !renderer.NoColor {
				segment = "\x1b[1;31m" + segment + "\x1b[0m"
			}
			lines[row] += segment
			if light < 4 {
				lines[row] += "  "
			}
		}
	}
	return lines[:]
}

func (renderer Renderer) laneLine(state *screenState, index, inner int) string {
	lane := state.lanes[index]
	selector := "  "
	if index == state.selected {
		selector = "> "
	}
	label := fmt.Sprintf("%s%-8s", selector, lane.name)
	trackWidth := max(8, inner-visibleWidth(label)-6)
	track := renderTrack(lane.progress, trackWidth, state.frame, state.checkered, lane.dnf)
	line := fmt.Sprintf("%s %s %3.0f%%", label, track, lane.progress*100)
	if lane.dnf {
		line = fmt.Sprintf("%s %s DNF", label, track)
	}
	return renderer.colorizeLane(line, index, lane.name)
}

func (renderer Renderer) statusLine(state *screenState) string {
	elapsed := time.Duration(0)
	if !state.started.IsZero() {
		elapsed = time.Since(state.started)
	}
	best, second := -1, -1
	for index, lane := range state.lanes {
		if !lane.finished || lane.dnf {
			continue
		}
		if best < 0 || lane.elapsed < state.lanes[best].elapsed {
			second, best = best, index
		} else if second < 0 || lane.elapsed < state.lanes[second].elapsed {
			second = index
		}
	}
	if best >= 0 {
		gap := time.Duration(0)
		if second >= 0 {
			gap = state.lanes[second].elapsed - state.lanes[best].elapsed
		}
		last := ""
		if state.lastFinished >= 0 {
			lastLane := state.lanes[state.lastFinished]
			if lastLane.dnf {
				last = fmt.Sprintf("   last: %s DNF", lastLane.name)
			} else {
				lastGap := max(time.Duration(0), lastLane.elapsed-state.lanes[best].elapsed)
				last = fmt.Sprintf("   last: %s +%.1fms", lastLane.name, float64(lastGap)/float64(time.Millisecond))
			}
		}
		return fmt.Sprintf("time %s   leader: %s   gap +%.1fms%s", formatDuration(elapsed), state.lanes[best].name, float64(gap)/float64(time.Millisecond), last)
	}
	leader := "-"
	for _, lane := range state.lanes {
		if leader == "-" || lane.progress > progressFor(state.lanes, leader) {
			leader = lane.name
		}
	}
	return fmt.Sprintf("time %s   pace: %s", formatDuration(elapsed), leader)
}

func progressFor(lanes []laneState, name string) float64 {
	for _, lane := range lanes {
		if lane.name == name {
			return lane.progress
		}
	}
	return 0
}

func (renderer Renderer) writeResult(state *screenState, width int) error {
	inner := width - 4
	lines := []string{border(width, '┌', '─', '┐'), boxLine("FINISH - "+state.result.Name, inner)}
	if state.resultReady {
		if len(state.result.Summary) > 0 && !state.result.Summary[0].DNF {
			winner := fmt.Sprintf("WINNER: %s takes the checkered flag!", state.result.Summary[0].Name)
			winnerLane := state.result.Summary[0]
			lines = append(lines, boxLine(renderer.colorizeLane(winner, winnerLane.Index, winnerLane.Name), inner))
		}
		lines = append(lines, boxLine(fmt.Sprintf("GOMAXPROCS=%d | median of %d trials", state.result.Cores, len(state.result.Trials)), inner))
		lines = append(lines, border(width, '├', '─', '┤'))
		best := time.Duration(0)
		if len(state.result.Summary) > 0 {
			best = state.result.Summary[0].Elapsed
		}
		for index, lane := range state.result.Summary {
			prefix := fmt.Sprintf("#%-2d %-10s", index+1, lane.Name)
			if lane.DNF {
				prefix = fmt.Sprintf("DNF %-10s", lane.Name)
				lines = append(lines, boxLine(renderer.colorizeLane(fmt.Sprintf("%s  DNF (exceeded -max)", prefix), lane.Index, lane.Name), inner))
				continue
			}
			barWidth := 20
			if lane.Elapsed > 0 && best > 0 {
				barWidth = max(1, int(20*float64(best)/float64(lane.Elapsed)))
			}
			entry := fmt.Sprintf("%s  %s  %s", prefix, formatDuration(lane.Elapsed), strings.Repeat("#", barWidth))
			lines = append(lines, boxLine(renderer.colorizeLane(entry, lane.Index, lane.Name), inner))
		}
		lines = append(lines, border(width, '├', '─', '┤'))
		lines = append(lines, boxLine(state.result.Note(), inner))
	} else {
		lines = append(lines, boxLine(state.comment, inner))
	}
	lines = append(lines, boxLine("PRESS ANY KEY TO CONTINUE | CTRL-C quits", inner))
	lines = append(lines, border(width, '└', '─', '┘'))
	return renderer.writeFrame(lines, width)
}

func (renderer Renderer) writePlainResult(state *screenState) error {
	if _, err := fmt.Fprintf(renderer.Out, "FINISH - %s\n", state.result.Name); err != nil {
		return err
	}
	fmt.Fprintf(renderer.Out, "GOMAXPROCS=%d | median of %d trials\n", state.result.Cores, len(state.result.Trials))
	best := time.Duration(0)
	if len(state.result.Summary) > 0 {
		best = state.result.Summary[0].Elapsed
	}
	for index, lane := range state.result.Summary {
		if lane.DNF {
			fmt.Fprintf(renderer.Out, "DNF %-10s DNF (exceeded -max)\n", lane.Name)
			continue
		}
		barWidth := 20
		if lane.Elapsed > 0 && best > 0 {
			barWidth = max(1, int(20*float64(best)/float64(lane.Elapsed)))
		}
		fmt.Fprintf(renderer.Out, "#%-2d %-10s %s %s\n", index+1, lane.Name, formatDuration(lane.Elapsed), strings.Repeat("#", barWidth))
	}
	fmt.Fprintf(renderer.Out, "note: %s\n", state.result.Note())
	return nil
}

func (renderer Renderer) writeSmall(width, height int) error {
	message := fmt.Sprintf("Enlarge terminal to at least %dx%d (now %dx%d).", minimumWidth, minimumHeight, width, height)
	if width < 1 {
		return nil
	}
	if len([]rune(message)) > width {
		message = fitText("Terminal too small; enlarge window.", width)
	}
	_, err := fmt.Fprintf(renderer.Out, "\x1b[H\x1b[2K%s", message)
	return err
}

func (renderer Renderer) writeFrame(lines []string, width int) error {
	var output strings.Builder
	output.WriteString("\x1b[H")
	for _, line := range lines {
		output.WriteString("\x1b[2K")
		output.WriteString(line)
		output.WriteString("\r\n")
	}
	_, err := io.WriteString(renderer.Out, output.String())
	return err
}

// Vehicle ideas:
// Current: `\ō͡≡o˞̶`
// Previous: `🏎`
const vehicleSprite = `\ō͡≡o˞̶`

func renderTrack(fraction float64, width, frame int, checkered, dnf bool) string {
	if width < 1 {
		return ""
	}
	track := make([]string, width)
	for index := range track {
		track[index] = " "
	}
	track[0] = "|"
	finish := width - 1
	track[finish] = "|"
	if checkered && width > 1 {
		track[width-2] = "#"
		track[width-1] = "#"
	}
	carWidth := visibleWidth(vehicleSprite)
	if width < carWidth+3 {
		return strings.Join(track, "")
	}
	maximum := max(0, width-carWidth-3)
	position := 1 + int(math.Max(0, math.Min(float64(maximum), fraction*float64(maximum))))
	for index := 1; index < position; index++ {
		track[index] = "_"
	}
	if dnf && position > 0 {
		track[position-1] = "~"
		if position > 2 {
			track[position-2] = "~"
		}
	}
	track[position] = vehicleSprite
	for index := 1; index < carWidth; index++ {
		track[position+index] = ""
	}
	return strings.Join(track, "")
}

func border(width int, left, fill, right rune) string {
	return string(left) + strings.Repeat(string(fill), max(0, width-2)) + string(right)
}

func boxLine(content string, inner int) string {
	if visibleWidth(content) > inner {
		content = fitText(stripANSI(content), inner)
	}
	return "│ " + content + strings.Repeat(" ", max(0, inner-visibleWidth(content))) + " │"
}

func fitText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) > width {
		return string(runes[:width])
	}
	return text
}

func visibleWidth(text string) int {
	width := 0
	for index := 0; index < len(text); {
		if text[index] == 0x1b && index+1 < len(text) && text[index+1] == '[' {
			index += 2
			for index < len(text) && text[index] != 'm' {
				index++
			}
			if index < len(text) {
				index++
			}
			continue
		}
		character, size := utf8.DecodeRuneInString(text[index:])
		if size == 0 {
			break
		}
		index += size
		if unicode.Is(unicode.Mn, character) || unicode.Is(unicode.Me, character) {
			continue
		}
		if character == '🏎' {
			width += 2
		} else {
			width++
		}
	}
	return width
}

func stripANSI(text string) string {
	var output strings.Builder
	for index := 0; index < len(text); {
		if text[index] == 0x1b && index+1 < len(text) && text[index+1] == '[' {
			index += 2
			for index < len(text) && text[index] != 'm' {
				index++
			}
			if index < len(text) {
				index++
			}
			continue
		}
		_, size := utf8.DecodeRuneInString(text[index:])
		output.WriteString(text[index : index+size])
		index += size
	}
	return output.String()
}

func (renderer Renderer) colorize(text string, lane int) string {
	return renderer.colorizeLane(text, lane, "")
}

func (renderer Renderer) colorizeLane(text string, lane int, name string) string {
	if renderer.NoColor || lane < 0 {
		return text
	}
	color := laneColors[lane%len(laneColors)]
	switch name {
	case "2-WRK":
		color = 46
	case "4-WRK":
		color = 250
	case "16-WRK":
		color = 196
	case "64-WRK":
		color = 208
	}
	vehicle := strings.ReplaceAll(vehicleSprite, "ō͡", fmt.Sprintf("\x1b[38;5;240mō͡\x1b[38;5;%dm", color))
	vehicle = strings.ReplaceAll(vehicle, "o˞̶", fmt.Sprintf("\x1b[38;5;240mo˞̶\x1b[38;5;%dm", color))
	text = strings.ReplaceAll(text, vehicleSprite, vehicle)
	return fmt.Sprintf("\x1b[38;5;%dm%s\x1b[0m", color, text)
}

func formatDuration(duration time.Duration) string {
	return fmt.Sprintf("%.3fs", duration.Seconds())
}

func replayDuration(result race.Result) time.Duration {
	longest := time.Duration(0)
	for _, lane := range result.Summary {
		if !lane.DNF && lane.Elapsed > longest {
			longest = lane.Elapsed
		}
	}
	if longest <= 0 {
		return minimumReplayDuration
	}
	if longest >= maximumReplayDuration/replayFactor {
		return maximumReplayDuration
	}
	return max(minimumReplayDuration, longest*replayFactor)
}

func prepareReplay(state *screenState, result race.Result) {
	state.replay = true
	state.startProgressReached = false
	state.checkered = false
	state.trial = 1
	state.trials = 1
	state.started = time.Now()
	state.lastFinished = -1
	state.selected = 0
	for index := range state.lanes {
		state.lanes[index].progress = 0
		state.lanes[index].elapsed = 0
		state.lanes[index].finished = false
		state.lanes[index].dnf = true
	}
	winnerFound := false
	for _, lane := range result.Summary {
		if lane.Index < 0 || lane.Index >= len(state.lanes) {
			continue
		}
		state.lanes[lane.Index].elapsed = lane.Elapsed
		state.lanes[lane.Index].dnf = lane.DNF
		if !lane.DNF && !winnerFound {
			state.selected = lane.Index
			winnerFound = true
		}
	}
}

func advanceReplay(state *screenState, elapsed, duration time.Duration) {
	longest := time.Duration(0)
	for _, lane := range state.lanes {
		if !lane.dnf && lane.elapsed > longest {
			longest = lane.elapsed
		}
	}
	if longest <= 0 || duration <= 0 {
		return
	}
	fraction := math.Min(float64(elapsed)/float64(duration), 1)
	for index := range state.lanes {
		lane := &state.lanes[index]
		previouslyFinished := lane.finished
		if lane.dnf {
			lane.progress = fraction * 0.85
			lane.finished = fraction >= 1
		} else {
			finishAt := float64(lane.elapsed) / float64(longest)
			lane.progress = math.Min(fraction/finishAt, 1)
			lane.finished = lane.progress >= 1
		}
		if !lane.dnf && lane.progress >= replayGoTarget {
			state.startProgressReached = true
		}
		if lane.finished && !previouslyFinished {
			state.lastFinished = index
		}
	}
}

func raceCores() int {
	return runtimeGOMAXPROCS()
}

func runtimeGOMAXPROCS() int {
	return runtime.GOMAXPROCS(0)
}
