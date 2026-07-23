package framework

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var DatabaseKey = NewServiceKey[*Database]("framework.database")

var ErrInvalidDatabaseProviderOptions = errors.New("framework: database provider options are invalid")

type DatabaseProviderOptions struct {
	PingTimeout time.Duration
}

func DefaultDatabaseProviderOptions() DatabaseProviderOptions {
	return DatabaseProviderOptions{PingTimeout: 5 * time.Second}
}

type DatabaseServiceProvider struct {
	pool     *sql.DB
	options  DatabaseProviderOptions
	database *Database
}

func NewDatabaseServiceProvider(
	pool *sql.DB,
	options DatabaseProviderOptions,
) *DatabaseServiceProvider {
	return &DatabaseServiceProvider{pool: pool, options: options}
}

func (provider *DatabaseServiceProvider) ProviderName() string {
	return "framework.database"
}

func (provider *DatabaseServiceProvider) Register(application *Application) error {
	database, err := NewDatabase(provider.pool)
	if err != nil {
		return err
	}
	provider.database = database
	if provider.options.PingTimeout < 0 {
		return ErrInvalidDatabaseProviderOptions
	}
	return Instance(application.Container(), DatabaseKey, database)
}

func (provider *DatabaseServiceProvider) Boot(*Application) error {
	if provider.database == nil {
		return ErrDatabaseUnavailable
	}
	ctx := context.Background()
	cancel := func() {}
	if provider.options.PingTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, provider.options.PingTimeout)
	}
	defer cancel()
	return provider.database.Ping(ctx)
}

func (provider *DatabaseServiceProvider) Terminate(
	ctx context.Context,
	_ *Application,
) error {
	if provider.database == nil {
		return nil
	}
	return provider.database.Close(ctx)
}
