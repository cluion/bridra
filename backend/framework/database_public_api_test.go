package framework_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

const publicDatabaseDriverName = "bridra_framework_public_database_test"

var registerPublicDatabaseDriver sync.Once

type publicDatabaseDriver struct{}

func (publicDatabaseDriver) Open(string) (driver.Conn, error) {
	return publicDatabaseConnection{}, nil
}

type publicDatabaseConnection struct{}

func (publicDatabaseConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (publicDatabaseConnection) Close() error {
	return nil
}

func (publicDatabaseConnection) Begin() (driver.Tx, error) {
	return publicDatabaseTransaction{}, nil
}

func (publicDatabaseConnection) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return publicDatabaseTransaction{}, nil
}

func (publicDatabaseConnection) Ping(context.Context) error {
	return nil
}

func (publicDatabaseConnection) ExecContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}

type publicDatabaseTransaction struct{}

func (publicDatabaseTransaction) Commit() error {
	return nil
}

func (publicDatabaseTransaction) Rollback() error {
	return nil
}

type publicAccountRepository struct {
	database *framework.Database
}

func (repository publicAccountRepository) Create(ctx context.Context, name string) error {
	executor, err := repository.database.Executor(ctx)
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, "INSERT INTO accounts (name) VALUES (?)", name)
	return err
}

func TestPublicDatabaseProviderTransactionAndRepositoryAPI(t *testing.T) {
	registerPublicDatabaseDriver.Do(func() {
		sql.Register(publicDatabaseDriverName, publicDatabaseDriver{})
	})
	pool, err := sql.Open(publicDatabaseDriverName, "public")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	provider := framework.NewDatabaseServiceProvider(
		pool,
		framework.DefaultDatabaseProviderOptions(),
	)
	var _ framework.ServiceProvider = provider
	var _ framework.BootableServiceProvider = provider
	var _ framework.TerminableServiceProvider = provider

	application := framework.NewApplication(nil)
	if err := application.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	database, err := framework.Resolve(application.Container(), framework.DatabaseKey)
	if err != nil {
		t.Fatalf("resolve database: %v", err)
	}
	repository := publicAccountRepository{database: database}
	if err := database.WithinTransaction(context.Background(), nil, func(ctx context.Context) error {
		return repository.Create(ctx, "Bridra")
	}); err != nil {
		t.Fatalf("transaction: %v", err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
