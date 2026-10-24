package track

import (
	"strings"
	"time"

	"gogogo/internal/race"

	"testing"
)

func TestRenderTrackClampsAndLeavesFinishMarker(t *testing.T) {
	track := renderTrack(1, 20, 0, true, false)
	if got := visibleWidth(track); got != 20 {
		t.Fatalf("track width = %d, want 20", got)
	}
	if !strings.HasSuffix(track, "##") {
		t.Fatalf("track does not retain the checkered finish: %q", track)
	}
}

func TestBoxLineFitsItsWidth(t *testing.T) {
	line := boxLine("a long line that must be clipped", 12)
	if got, want := visibleWidth(line), 16; got != want {
		t.Fatalf("visible width = %d, want %d", got, want)
	}
}

func TestRenderTrackShowsFormulaCar(t *testing.T) {
	track := renderTrack(0.5, 32, 0, false, false)
	position := strings.Index(track, vehicleSprite)
	if position < 0 {
		t.Fatalf("track does not show the requested race car: %q", track)
	}
	if !strings.Contains(track[:position], "_") {
		t.Fatalf("track does not show a trail behind the car: %q", track)
	}
	if strings.Contains(track[position+len(vehicleSprite):], "_") {
		t.Fatalf("track shows ground in front of the car: %q", track)
	}
	if got := visibleWidth(track); got != 32 {
		t.Fatalf("visible track width = %d, want 32", got)
	}
	if track[len(track)-1] != '|' {
		t.Fatalf("track does not retain the finish marker: %q", track)
	}
}

func TestLaneLineKeepsVehicleAndTrailTogether(t *testing.T) {
	renderer := Renderer{NoColor: true}
	state := screenState{lanes: []laneState{{name: "SEQ-1", progress: 0.5}}}
	line := renderer.laneLine(&state, 0, 56)
	if !strings.Contains(line, vehicleSprite) || !strings.Contains(line, "_") {
		t.Fatalf("lane line = %q", line)
	}
}

func TestLaneColorsFollowWorkerNames(t *testing.T) {
	renderer := Renderer{}
	for _, test := range []struct {
		name  string
		color string
	}{
		{name: "2-WRK", color: "46"},
		{name: "4-WRK", color: "250"},
		{name: "16-WRK", color: "196"},
		{name: "64-WRK", color: "208"},
	} {
		line := renderer.colorizeLane(test.name, 0, test.name)
		if !strings.HasPrefix(line, "\x1b[38;5;"+test.color+"m") {
			t.Errorf("%s color = %q, want ANSI color %s", test.name, line, test.color)
		}
	}
}

func TestLaneLineColorsTyresDarkGray(t *testing.T) {
	state := screenState{lanes: []laneState{{name: "SEQ-1", progress: 0.5}}}
	line := (Renderer{}).laneLine(&state, 0, 56)
	for _, expected := range []string{
		"\x1b[38;5;240mō͡\x1b[38;5;39m",
		"\x1b[38;5;240mo˞̶\x1b[38;5;39m",
	} {
		if !strings.Contains(line, expected) {
			t.Errorf("lane line does not color tyre with %q: %q", expected, line)
		}
	}
	if strings.Contains(line, "ō\x1b") || strings.Contains(line, "o\x1b") {
		t.Fatalf("ANSI color split a car glyph from its combining marks: %q", line)
	}
}

func TestStartSequenceLightsThenShowsGoArt(t *testing.T) {
	renderer := Renderer{NoColor: true}
	lines, ready := renderer.startSequenceLines(0)
	if ready || strings.Count(lines[2], "|#####|") != 1 {
		t.Fatalf("first light stage = %q, ready=%v", lines[2], ready)
	}
	lines, ready = renderer.startSequenceLines(2 * startLightInterval)
	if ready || strings.Count(lines[2], "|#####|") != 3 {
		t.Fatalf("third light stage = %q, ready=%v", lines[2], ready)
	}
	lines, ready = renderer.startSequenceLines(5*startLightInterval + startLightsHold)
	if ready || strings.Join(lines[:len(goGoGoArt)], "\n") != strings.Join(goGoGoArt, "\n") || lines[len(goGoGoArt)] != "" {
		t.Fatalf("go stage = %q, ready=%v", lines, ready)
	}
	_, ready = renderer.startSequenceLines(5*startLightInterval + startLightsHold + startGoHold)
	if !ready {
		t.Fatal("countdown did not complete after its GO hold")
	}
}

func TestWriteLiveKeepsBannerSpaceAfterGo(t *testing.T) {
	var output strings.Builder
	renderer := Renderer{Out: &output, NoColor: true, StartSequence: true}
	state := screenState{
		lanes:                 []laneState{{name: "SEQ-1", progress: 0.1}},
		startSequenceStarted:  time.Now().Add(-startSequenceDuration),
		startSequenceComplete: true,
		replay:                true,
		startReplayGoStarted:  time.Now(),
	}
	if err := renderer.writeLive(&state, 80, 24); err != nil {
		t.Fatalf("writeLive() during GO error = %v", err)
	}
	goFrame := output.String()
	state.startProgressReached = true
	output.Reset()
	if err := renderer.writeLive(&state, 80, 24); err != nil {
		t.Fatalf("writeLive() after GO error = %v", err)
	}
	blankFrame := output.String()
	if strings.Count(goFrame, "\r\n") != strings.Count(blankFrame, "\r\n") {
		t.Fatalf("frame height changed after GO: before=%d rows, after=%d rows", strings.Count(goFrame, "\r\n"), strings.Count(blankFrame, "\r\n"))
	}
	if !strings.Contains(goFrame, goGoGoArt[0]) || strings.Contains(blankFrame, goGoGoArt[0]) {
		t.Fatalf("GO art should disappear after the replay threshold: visible=%v blank=%v", strings.Contains(goFrame, goGoGoArt[0]), strings.Contains(blankFrame, goGoGoArt[0]))
	}
}

func TestCountdownHidesLiveTrackUntilReplay(t *testing.T) {
	var output strings.Builder
	renderer := Renderer{Out: &output, NoColor: true, StartSequence: true}
	state := screenState{
		lanes:                []laneState{{name: "SEQ-1", progress: 0.75}},
		startSequenceStarted: time.Now(),
	}
	if err := renderer.writeLive(&state, 80, 24); err != nil {
		t.Fatalf("writeLive() during countdown error = %v", err)
	}
	if strings.Contains(output.String(), vehicleSprite) {
		t.Fatal("live track appeared during countdown")
	}
	state.replay = true
	output.Reset()
	if err := renderer.writeLive(&state, 80, 24); err != nil {
		t.Fatalf("writeLive() during replay error = %v", err)
	}
	if !strings.Contains(output.String(), vehicleSprite) {
		t.Fatal("replay track did not appear")
	}
}

func TestGoBannerFlashesUntilFirstReplayLaneReachesFiftyPercent(t *testing.T) {
	renderer := Renderer{NoColor: true}
	for _, stage := range []struct {
		elapsed time.Duration
		visible bool
	}{
		{elapsed: 0, visible: true},
		{elapsed: startGoFlashInterval - time.Nanosecond, visible: true},
		{elapsed: startGoFlashInterval, visible: false},
		{elapsed: 2*startGoFlashInterval - time.Nanosecond, visible: false},
		{elapsed: 2 * startGoFlashInterval, visible: true},
	} {
		lines := renderer.flashingGoGoGoLines(stage.elapsed)
		gotVisible := strings.Join(lines[:len(goGoGoArt)], "\n") == strings.Join(goGoGoArt, "\n")
		if gotVisible != stage.visible {
			t.Errorf("flash at %s visible = %v, want %v", stage.elapsed, gotVisible, stage.visible)
		}
	}

	state := screenState{replay: true, lanes: []laneState{
		{elapsed: time.Second},
		{elapsed: 2 * time.Second},
	}}
	state.apply(race.Event{Kind: race.EventProgress, Lane: 0, Fraction: 0.9})
	if state.startProgressReached {
		t.Fatal("live race progress should not stop the replay GO banner")
	}
	advanceReplay(&state, 2499*time.Millisecond, 10*time.Second)
	if state.startProgressReached {
		t.Fatal("GO banner stopped before a replay lane reached 50%")
	}
	advanceReplay(&state, 2500*time.Millisecond, 10*time.Second)
	if !state.startProgressReached {
		t.Fatal("GO banner did not stop when a replay lane reached 50%")
	}
}

func TestReplayDurationScalesAndCapsRaceTime(t *testing.T) {
	result := race.Result{Summary: []race.LaneResult{{Elapsed: 750 * time.Millisecond}}}
	if got, want := replayDuration(result), 15*time.Second; got != want {
		t.Fatalf("replayDuration() = %s, want %s", got, want)
	}
	result.Summary[0].Elapsed = 2 * time.Second
	if got, want := replayDuration(result), maximumReplayDuration; got != want {
		t.Fatalf("replayDuration() = %s, want cap %s", got, want)
	}
}

func TestAdvanceReplayFinishesLanesByMeasuredTime(t *testing.T) {
	state := screenState{lanes: []laneState{
		{elapsed: 100 * time.Millisecond},
		{elapsed: 200 * time.Millisecond},
	}}
	advanceReplay(&state, 5*time.Second, 10*time.Second)
	if !state.lanes[0].finished || state.lanes[0].progress != 1 {
		t.Fatalf("fast lane = %#v, want finished at the line", state.lanes[0])
	}
	if state.lanes[1].finished || state.lanes[1].progress != 0.5 {
		t.Fatalf("slow lane = %#v, want halfway to the line", state.lanes[1])
	}
}

func TestPrepareReplayLoadsAllLanesAndSelectsWinner(t *testing.T) {
	state := screenState{lanes: make([]laneState, 3)}
	result := race.Result{Summary: []race.LaneResult{
		{Index: 2, Name: "winner", Elapsed: time.Second},
		{Index: 0, Name: "second", Elapsed: 2 * time.Second},
	}}
	prepareReplay(&state, result)
	if state.selected != 2 || state.started.IsZero() {
		t.Fatalf("replay state selected=%d started=%v", state.selected, state.started)
	}
	if state.lanes[0].elapsed != 2*time.Second || state.lanes[2].elapsed != time.Second {
		t.Fatalf("replay lane timings = %#v", state.lanes)
	}
	if !state.lanes[1].dnf {
		t.Fatalf("lane without a result should remain DNF: %#v", state.lanes[1])
	}
}

func TestWriteResultCallsOutWinnerAndWaitsForKey(t *testing.T) {
	var output strings.Builder
	renderer := Renderer{Out: &output, NoColor: true}
	state := screenState{
		resultReady: true,
		result: race.Result{
			Name: "The Sum Sprint",
			Summary: []race.LaneResult{
				{Name: "2-WRK", Elapsed: time.Second},
				{Name: "SEQ-1", Elapsed: 2 * time.Second},
			},
		},
	}
	if err := renderer.writeResult(&state, 80); err != nil {
		t.Fatalf("writeResult() error = %v", err)
	}
	for _, text := range []string{"WINNER: 2-WRK takes the checkered flag!", "PRESS ANY KEY TO CONTINUE"} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("result screen does not contain %q", text)
		}
	}
}

func TestReadKeysRecognizesAnyCharacter(t *testing.T) {
	keys := make(chan Key, 1)
	ReadKeys(strings.NewReader("x"), keys)
	if key, ok := <-keys; !ok || key != KeyAny {
		t.Fatalf("ReadKeys() = %v, %v; want KeyAny", key, ok)
	}
}
