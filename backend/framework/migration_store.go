package framework

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

var ErrInvalidSQLMigrationStoreOptions = errors.New("framework: SQL migration store options are invalid")

type SQLPlaceholderStyle string

const (
	SQLPlaceholderQuestionMark SQLPlaceholderStyle = "question-mark"
	SQLPlaceholderDollar       SQLPlaceholderStyle = "dollar"
)

type SQLMigrationStoreOptions struct {
	Table            string
	PlaceholderStyle SQLPlaceholderStyle
}

func DefaultSQLMigrationStoreOptions() SQLMigrationStoreOptions {
	return SQLMigrationStoreOptions{
		Table:            "bridra_migrations",
		PlaceholderStyle: SQLPlaceholderQuestionMark,
	}
}

type SQLMigrationStore struct {
	table            string
	placeholderStyle SQLPlaceholderStyle
}

var _ MigrationStore = (*SQLMigrationStore)(nil)

func NewSQLMigrationStore(options SQLMigrationStoreOptions) (*SQLMigrationStore, error) {
	defaults := DefaultSQLMigrationStoreOptions()
	if options.Table == "" {
		options.Table = defaults.Table
	}
	if options.PlaceholderStyle == "" {
		options.PlaceholderStyle = defaults.PlaceholderStyle
	}
	if !validSQLIdentifier(options.Table) ||
		(options.PlaceholderStyle != SQLPlaceholderQuestionMark &&
			options.PlaceholderStyle != SQLPlaceholderDollar) {
		return nil, ErrInvalidSQLMigrationStoreOptions
	}
	return &SQLMigrationStore{
		table:            options.Table,
		placeholderStyle: options.PlaceholderStyle,
	}, nil
}

func (store *SQLMigrationStore) Table() string {
	if store == nil {
		return ""
	}
	return store.table
}

func (store *SQLMigrationStore) Ensure(ctx context.Context, executor SQLExecutor) error {
	if store == nil || executor == nil {
		return ErrMigrationStoreUnavailable
	}
	if ctx == nil {
		return ErrMigrationContextUnavailable
	}
	_, err := executor.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (version VARCHAR(255) PRIMARY KEY, name VARCHAR(255) NOT NULL, batch BIGINT NOT NULL)",
		store.table,
	))
	return err
}

func (store *SQLMigrationStore) Applied(
	ctx context.Context,
	executor SQLExecutor,
) ([]AppliedMigration, error) {
	if store == nil || executor == nil {
		return nil, ErrMigrationStoreUnavailable
	}
	if ctx == nil {
		return nil, ErrMigrationContextUnavailable
	}
	rows, err := executor.QueryContext(ctx, fmt.Sprintf(
		"SELECT version, name, batch FROM %s ORDER BY version ASC",
		store.table,
	))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	migrations := make([]AppliedMigration, 0)
	for rows.Next() {
		var migration AppliedMigration
		if err := rows.Scan(&migration.Version, &migration.Name, &migration.Batch); err != nil {
			return nil, err
		}
		migrations = append(migrations, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return migrations, rows.Close()
}

func (store *SQLMigrationStore) Record(
	ctx context.Context,
	executor SQLExecutor,
	migration AppliedMigration,
) error {
	if store == nil || executor == nil {
		return ErrMigrationStoreUnavailable
	}
	if ctx == nil {
		return ErrMigrationContextUnavailable
	}
	_, err := executor.ExecContext(ctx, fmt.Sprintf(
		"INSERT INTO %s (version, name, batch) VALUES (%s, %s, %s)",
		store.table,
		store.placeholder(1),
		store.placeholder(2),
		store.placeholder(3),
	), migration.Version, migration.Name, migration.Batch)
	return err
}

func (store *SQLMigrationStore) Remove(
	ctx context.Context,
	executor SQLExecutor,
	version string,
) error {
	if store == nil || executor == nil {
		return ErrMigrationStoreUnavailable
	}
	if ctx == nil {
		return ErrMigrationContextUnavailable
	}
	_, err := executor.ExecContext(ctx, fmt.Sprintf(
		"DELETE FROM %s WHERE version = %s",
		store.table,
		store.placeholder(1),
	), version)
	return err
}

func (store *SQLMigrationStore) placeholder(index int) string {
	if store.placeholderStyle == SQLPlaceholderDollar {
		return "$" + strconv.Itoa(index)
	}
	return "?"
}

func validSQLIdentifier(identifier string) bool {
	if identifier == "" {
		return false
	}
	for index, character := range identifier {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') || character == '_' ||
			(index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}
