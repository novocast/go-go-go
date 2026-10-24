package broker

import "gogogo/internal/race"

func FanOut(input <-chan race.Event, outputs ...chan race.Event) {
	defer func() {
		for _, output := range outputs {
			close(output)
		}
	}()
	for event := range input {
		for _, output := range outputs {
			if event.Kind == race.EventProgress {
				select {
				case output <- event:
				default:
				}
				continue
			}
			output <- event
		}
	}
}