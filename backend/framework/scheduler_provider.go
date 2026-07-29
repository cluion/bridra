package framework

import (
	"context"
	"errors"
)

var SchedulerKey = NewServiceKey[*Scheduler]("framework.scheduler")

type SchedulerServiceProvider struct {
	options   SchedulerOptions
	scheduler *Scheduler
}

func NewSchedulerServiceProvider(options SchedulerOptions) *SchedulerServiceProvider {
	return &SchedulerServiceProvider{options: options}
}

func (provider *SchedulerServiceProvider) ProviderName() string {
	return "framework.scheduler"
}

func (provider *SchedulerServiceProvider) Register(application *Application) error {
	scheduler, err := NewScheduler(provider.options)
	if err != nil {
		return err
	}
	if err := Instance(application.Container(), SchedulerKey, scheduler); err != nil {
		return err
	}
	provider.scheduler = scheduler
	return nil
}

func (provider *SchedulerServiceProvider) Boot(*Application) error {
	if provider.scheduler == nil {
		return ErrSchedulerUnavailable
	}
	return provider.scheduler.Start()
}

func (provider *SchedulerServiceProvider) Terminate(
	ctx context.Context,
	_ *Application,
) error {
	if provider.scheduler == nil {
		return nil
	}
	if err := provider.scheduler.Shutdown(ctx); err != nil {
		return err
	}
	closer, ok := provider.options.Store.(interface{ Close() error })
	if !ok {
		return nil
	}
	if err := closer.Close(); err != nil {
		return errors.Join(ErrSchedulerStoreOperationFailed, err)
	}
	return nil
}
