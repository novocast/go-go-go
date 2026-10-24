package commentary

import (
	"testing"

	"gogogo/internal/race"
)

func TestCommentaryIncludesStartFinishAndDNF(t *testing.T) {
	events := make(chan race.Event, 3)
	lines := make(chan string, 4)
	events <- race.Event{Kind: race.EventRaceStarted}
	events <- race.Event{Kind: race.EventFinished, LaneName: "SEQ-1"}
	events <- race.Event{Kind: race.EventDNF, LaneName: "64-WRK"}
	close(events)
	Run(events, lines)
	if count := len(lines); count != 3 {
		t.Fatalf("got %d commentary lines, want 3", count)
	}
}