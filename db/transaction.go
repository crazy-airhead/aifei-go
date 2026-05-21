package db

import "database/sql"

// TransactionWithID executes a function in a transaction for the given config ID.
func TransactionWithID(configID string, fn func() error) error {
	config := GetConfig(configID)
	pool, err := config.Pool()
	if err != nil {
		return err
	}
	tx, err := pool.Begin()
	if err != nil {
		return err
	}
	if err := fn(); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// TxBegin begins a transaction and returns *sql.Tx.
func TxBegin(configID ...string) (*sql.Tx, error) {
	config := GetConfig(configID...)
	pool, err := config.Pool()
	if err != nil {
		return nil, err
	}
	return pool.Begin()
}
