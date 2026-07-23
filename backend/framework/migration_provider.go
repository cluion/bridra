package framework

var MigrationRunnerKey = NewServiceKey[*MigrationRunner]("framework.migration-runner")

type MigrationServiceProvider struct {
	store MigrationStore
}

func NewMigrationServiceProvider(store MigrationStore) *MigrationServiceProvider {
	return &MigrationServiceProvider{store: store}
}

func (provider *MigrationServiceProvider) ProviderName() string {
	return "framework.migrations"
}

func (provider *MigrationServiceProvider) Register(application *Application) error {
	database, err := Resolve(application.Container(), DatabaseKey)
	if err != nil {
		return err
	}
	runner, err := NewMigrationRunner(database, provider.store)
	if err != nil {
		return err
	}
	if err := Instance(application.Container(), MigrationRunnerKey, runner); err != nil {
		return err
	}
	return nil
}
