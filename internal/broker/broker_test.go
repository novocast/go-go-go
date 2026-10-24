package broker

import (
	"testing"

	"gogogo/internal/race"
)

func TestFanOutPreservesCriticalEventsAndClosesOutputs(t *testing.T) {
	input := make(chan race.Event, 1)
	left := make(chan race.Event, 1)
	right := make(chan race.Event, 1)
	input <- race.Event{Kind: race.EventFinished, LaneName: "SEQ-1"}
	close(input)
	FanOut(input, left, right)
	for _, output := range []chan race.Event{left, right} {
		event, ok := <-output
		if !ok || event.Kind != race.EventFinished {
			t.Fatalf("critical event was not delivered: %#v, open=%v", event, ok)
		}
		if _, ok := <-output; ok {
			t.Fatal("output channel was not closed")
		}
	}
}