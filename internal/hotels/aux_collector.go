package hotels

import (
	"context"
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// hotelAuxTask is one independently bounded auxiliary-provider search. Its run
// function returns owned slices: workers never mutate state retained by the
// caller after the auxiliary budget expires.
type hotelAuxTask struct {
	id        string
	name      string
	breakerID string
	run       func(context.Context) hotelAuxOutcome
}

type hotelAuxBreakerAction uint8

const (
	hotelAuxBreakerNone hotelAuxBreakerAction = iota
	hotelAuxBreakerSuccess
	hotelAuxBreakerFailure
)

type hotelAuxOutcome struct {
	results       []models.HotelResult
	statuses      []models.ProviderStatus
	breakerAction hotelAuxBreakerAction
}

type indexedHotelAuxOutcome struct {
	index   int
	outcome hotelAuxOutcome
}

func applyHotelAuxBreakerOutcome(task hotelAuxTask, outcome hotelAuxOutcome) {
	if task.breakerID == "" {
		return
	}
	switch outcome.breakerAction {
	case hotelAuxBreakerSuccess:
		hotelBreaker.RecordSuccess(task.breakerID)
	case hotelAuxBreakerFailure:
		hotelBreaker.RecordFailure(task.breakerID)
	}
}

// collectHotelAuxTasks returns one outcome per task, in declaration order. A
// buffered channel lets a non-cooperative worker finish after this function
// returns without blocking or writing into the caller's result state.
func collectHotelAuxTasks(ctx context.Context, timeout time.Duration, tasks []hotelAuxTask) []hotelAuxOutcome {
	if len(tasks) == 0 {
		return nil
	}

	auxCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	outcomeCh := make(chan indexedHotelAuxOutcome, len(tasks))
	for index, task := range tasks {
		go func() {
			outcomeCh <- indexedHotelAuxOutcome{index: index, outcome: task.run(auxCtx)}
		}()
	}

	outcomes := make([]hotelAuxOutcome, len(tasks))
	completed := make([]bool, len(tasks))
	remaining := len(tasks)
	for remaining > 0 {
		select {
		case received := <-outcomeCh:
			if completed[received.index] {
				continue
			}
			outcomes[received.index] = received.outcome
			completed[received.index] = true
			remaining--
		case <-auxCtx.Done():
			for index, task := range tasks {
				if completed[index] {
					continue
				}
				outcomes[index].statuses = []models.ProviderStatus{
					hotelProviderStatusFromError(task.id, task.name, fmt.Errorf("auxiliary provider budget exceeded: %w", auxCtx.Err())),
				}
				if task.breakerID != "" {
					outcomes[index].breakerAction = hotelAuxBreakerFailure
				}
			}
			return outcomes
		}
	}
	return outcomes
}
