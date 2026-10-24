package commentary

import (
	"fmt"
	"math"
	"time"

	"gogogo/internal/race"
)

const cooldown = 1500 * time.Millisecond

func Run(events <-chan race.Event, lines chan<- string) {
	defer close(lines)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastLine := time.Time{}
	lastLeadChange := time.Now()
	lastIdleLine := time.Time{}
	fastest := make(map[int]time.Duration)
	progress := make(map[int]float64)
	finished := make(map[int][]time.Duration)
	halfAnnounced := make(map[int]bool)

	emit := func(line string, now time.Time, urgent bool) bool {
		if !urgent && !lastLine.IsZero() && now.Sub(lastLine) < cooldown {
			return false
		}
		select {
		case lines <- line:
			lastLine = now
			return true
		default:
			return false
		}
	}

	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			now := time.Now()
			switch event.Kind {
			case race.EventRaceStarted:
				lastLeadChange = now
				emit("And it's Go, go, go!", now, true)
			case race.EventTrialStarted:
				lastLeadChange = now
				fastest = make(map[int]time.Duration)
				progress = make(map[int]float64)
				finished = make(map[int][]time.Duration)
				halfAnnounced = make(map[int]bool)
			case race.EventProgress:
				if event.Fraction > progress[event.Lane] {
					progress[event.Lane] = event.Fraction
				}
				if event.Fraction >= 0.5 && !halfAnnounced[event.Lane] {
					halfAnnounced[event.Lane] = true
					emit("With half the race gone, there's half the race left!", now, false)
				}
			case race.EventFinished:
				previous, hadLeader := fastest[event.Trial]
				if !hadLeader || event.Elapsed < previous {
					if hadLeader {
						emit(fmt.Sprintf("...and %s takes it! I don't believe it!", event.LaneName), now, true)
					}
					fastest[event.Trial] = event.Elapsed
					lastLeadChange = now
				}
				for _, earlier := range finished[event.Trial] {
					if math.Abs(float64(event.Elapsed-earlier)) < float64(5*time.Millisecond) {
						emit("A photo finish!", now, true)
						break
					}
				}
				finished[event.Trial] = append(finished[event.Trial], event.Elapsed)
				emit(fmt.Sprintf("%s crosses the line in %.3fs!", event.LaneName, event.Elapsed.Seconds()), now, true)
				progressLeader := -1
				for lane, fraction := range progress {
					if progressLeader < 0 || fraction > progress[progressLeader] {
						progressLeader = lane
					}
				}
				if progressLeader >= 0 && progressLeader != event.Lane {
					emit(fmt.Sprintf("The clock disagrees with the cars: %s leads on time.", event.LaneName), now, true)
				}
			case race.EventDNF:
				emit(fmt.Sprintf("%s is out; there's nothing wrong with that car except that it's on fire.", event.LaneName), now, true)
			}
		case now := <-ticker.C:
			if !lastLeadChange.IsZero() && now.Sub(lastLeadChange) >= 2*time.Second && (lastIdleLine.IsZero() || now.Sub(lastIdleLine) >= 2*time.Second) {
				if emit("Unless I'm very much mistaken...", now, false) {
					lastIdleLine = now
				}
			}
		}
	}
}