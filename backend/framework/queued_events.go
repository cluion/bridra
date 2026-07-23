package framework

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

var (
	ErrInvalidQueuedEventMapper = errors.New("framework: queued event mapper is invalid")
	ErrQueuedEventMappingFailed = errors.New("framework: queued event mapping failed")
	ErrQueuedEventEnqueueFailed = errors.New("framework: queued event enqueue failed")
)

type EventJobMapper[E any, J any] func(context.Context, E) (J, error)

func ListenQueued[E any, J any](
	dispatcher *EventDispatcher,
	queue *JobQueue,
	name string,
	mapper EventJobMapper[E, J],
) error {
	if dispatcher == nil {
		return ErrEventDispatcherUnavailable
	}
	if queue == nil {
		return ErrJobQueueUnavailable
	}
	if mapper == nil {
		return ErrInvalidQueuedEventMapper
	}
	eventType := reflect.TypeFor[E]()
	jobType := reflect.TypeFor[J]()
	return Listen(dispatcher, name, func(ctx context.Context, event E) error {
		job, err := mapper(ctx, event)
		if err != nil {
			return fmt.Errorf(
				"%w: event %s to job %s: %w",
				ErrQueuedEventMappingFailed,
				eventType,
				jobType,
				err,
			)
		}
		if err := DispatchJob(ctx, queue, job); err != nil {
			return fmt.Errorf(
				"%w: event %s to job %s: %w",
				ErrQueuedEventEnqueueFailed,
				eventType,
				jobType,
				err,
			)
		}
		return nil
	})
}
