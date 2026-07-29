package framework

import (
	"context"
	"errors"
)

var JobQueueKey = NewServiceKey[*JobQueue]("framework.job-queue")

type QueueServiceProvider struct {
	options JobQueueOptions
	queue   *JobQueue
}

func NewQueueServiceProvider(options JobQueueOptions) *QueueServiceProvider {
	return &QueueServiceProvider{options: options}
}

func (provider *QueueServiceProvider) ProviderName() string {
	return "framework.queue"
}

func (provider *QueueServiceProvider) Register(application *Application) error {
	queue, err := NewJobQueue(provider.options)
	if err != nil {
		return err
	}
	if err := Instance(application.Container(), JobQueueKey, queue); err != nil {
		return err
	}
	provider.queue = queue
	return nil
}

func (provider *QueueServiceProvider) Boot(*Application) error {
	if provider.queue == nil {
		return ErrJobQueueUnavailable
	}
	return provider.queue.Start()
}

func (provider *QueueServiceProvider) Terminate(
	ctx context.Context,
	_ *Application,
) error {
	if provider.queue == nil {
		return nil
	}
	if err := provider.queue.Shutdown(ctx); err != nil {
		return err
	}
	closer, ok := provider.options.Store.(interface{ Close() error })
	if !ok {
		return nil
	}
	if err := closer.Close(); err != nil {
		return errors.Join(ErrJobStoreOperationFailed, err)
	}
	return nil
}
