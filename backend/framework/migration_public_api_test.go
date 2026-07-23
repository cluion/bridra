package framework_test

import (
	"context"
	"database/sql"
	"reflect"
	"sync"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

type publicMigrationStore struct {
	mu      sync.Mutex
	records []framework.AppliedMigration
}

func (*publicMigrationStore) Ensure(context.Context, framework.SQLExecutor) error {
	return nil
}

func (store *publicMigrationStore) Applied(
	context.Context,
	framework.SQLExecutor,
) ([]framework.AppliedMigration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]framework.AppliedMigration(nil), store.records...), nil
}

func (store *publicMigrationStore) Record(
	_ context.Context,
	_ framework.SQLExecutor,
	migration framework.AppliedMigration,
) error {
	store.mu.Lock()
	store.records = append(store.records, migration)
	store.mu.Unlock()
	return nil
}

func (store *publicMigrationStore) Remove(
	_ context.Context,
	_ framework.SQLExecutor,
	version string,
) error {
	store.mu.Lock()
	remaining := store.records[:0]
	for _, migration := range store.records {
		if migration.Version != version {
			remaining = append(remaining, migration)
		}
	}
	store.records = remaining
	store.mu.Unlock()
	return nil
}

type publicMigrationDefinitionProvider struct{}

func (publicMigrationDefinitionProvider) Register(application *framework.Application) error {
	runner, err := framework.Resolve(application.Container(), framework.MigrationRunnerKey)
	if err != nil {
		return err
	}
	return runner.Register(framework.Migration{
		Version: "202607220001",
		Name:    "create_accounts",
		Up: func(ctx context.Context, executor framework.SQLExecutor) error {
			_, err := executor.ExecContext(ctx, "CREATE TABLE accounts (id BIGINT)")
			return err
		},
		Down: func(ctx context.Context, executor framework.SQLExecutor) error {
			_, err := executor.ExecContext(ctx, "DROP TABLE accounts")
			return err
		},
	})
}

func TestPublicMigrationRunnerProviderAndStoreAPI(t *testing.T) {
	registerPublicDatabaseDriver.Do(func() {
		sql.Register(publicDatabaseDriverName, publicDatabaseDriver{})
	})
	pool, err := sql.Open(publicDatabaseDriverName, "migrations")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := &publicMigrationStore{}
	var _ framework.MigrationStore = store
	migrationProvider := framework.NewMigrationServiceProvider(store)
	var _ framework.ServiceProvider = migrationProvider

	application := framework.NewApplication(nil)
	if err := application.Register(
		framework.NewDatabaseServiceProvider(
			pool,
			framework.DefaultDatabaseProviderOptions(),
		),
		migrationProvider,
		publicMigrationDefinitionProvider{},
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	runner, err := framework.Resolve(application.Container(), framework.MigrationRunnerKey)
	if err != nil {
		t.Fatalf("resolve runner: %v", err)
	}
	result, err := runner.Migrate(context.Background())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	expected := []framework.AppliedMigration{{
		Version: "202607220001",
		Name:    "create_accounts",
		Batch:   1,
	}}
	if !reflect.DeepEqual(result.Applied, expected) {
		t.Fatalf("applied = %#v", result.Applied)
	}
	statuses, err := runner.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Applied || statuses[0].Batch != 1 {
		t.Fatalf("statuses = %#v", statuses)
	}
	rollback, err := runner.Rollback(context.Background())
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !reflect.DeepEqual(rollback.RolledBack, expected) {
		t.Fatalf("rolled back = %#v", rollback.RolledBack)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestPublicSQLMigrationStoreOptions(t *testing.T) {
	store, err := framework.NewSQLMigrationStore(framework.SQLMigrationStoreOptions{
		Table:            "application_migrations",
		PlaceholderStyle: framework.SQLPlaceholderDollar,
	})
	if err != nil {
		t.Fatalf("new SQL migration store: %v", err)
	}
	var _ framework.MigrationStore = store
	if store.Table() != "application_migrations" {
		t.Fatalf("table = %q", store.Table())
	}
}
