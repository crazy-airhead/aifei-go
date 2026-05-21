package db

import (
	"fmt"
)

// BatchResult holds batch operation results.
type BatchResult struct {
	RowsAffected int64
	Error        error
}

// Batch provides batch database operations.
type Batch struct {
	config *Config
}

// Insert batch-inserts rows.
func (b *Batch) Insert(rows []*Row) (*BatchResult, error) {
	if len(rows) == 0 {
		return &BatchResult{}, nil
	}
	return b.InsertWithTable(rows[0].table, rows)
}

// InsertWithTable batch-inserts rows for a specific table.
func (b *Batch) InsertWithTable(table string, rows []*Row) (*BatchResult, error) {
	if len(rows) == 0 {
		return &BatchResult{}, nil
	}
	pool, err := b.config.Pool()
	if err != nil {
		return nil, err
	}

	fields := rows[0].FieldNames()
	sqlStr := b.config.Dialect.ForInsert(table, fields)

	stmt, err := pool.Prepare(sqlStr)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var total int64
	for _, row := range rows {
		args := row.FieldValues()
		result, err := stmt.Exec(args...)
		if err != nil {
			return &BatchResult{RowsAffected: total, Error: err}, err
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return &BatchResult{RowsAffected: total}, nil
}

// Update batch-updates rows.
func (b *Batch) Update(rows []*Row) (*BatchResult, error) {
	if len(rows) == 0 {
		return &BatchResult{}, nil
	}
	return b.UpdateWithTable(rows[0].table, rows)
}

// UpdateWithTable batch-updates rows for a specific table.
func (b *Batch) UpdateWithTable(table string, rows []*Row) (*BatchResult, error) {
	pool, err := b.config.Pool()
	if err != nil {
		return nil, err
	}

	var total int64
	for _, row := range rows {
		changedFields := row.ChangedFields()
		if len(changedFields) == 0 {
			continue
		}
		args := make([]interface{}, 0, len(changedFields)+len(row.primaryKeys))
		for _, f := range changedFields {
			args = append(args, row.data[f])
		}
		for _, pk := range row.primaryKeys {
			args = append(args, row.data[pk])
		}

		sqlStr := b.config.Dialect.ForUpdate(table, changedFields, row.primaryKeys)
		result, err := pool.Exec(sqlStr, args...)
		if err != nil {
			return &BatchResult{RowsAffected: total, Error: err}, err
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return &BatchResult{RowsAffected: total}, nil
}

// Execute batch-executes the same SQL with different args.
func (b *Batch) Execute(sql string, argsList [][]interface{}) (*BatchResult, error) {
	pool, err := b.config.Pool()
	if err != nil {
		return nil, err
	}
	stmt, err := pool.Prepare(sql)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var total int64
	for _, args := range argsList {
		result, err := stmt.Exec(args...)
		if err != nil {
			return &BatchResult{RowsAffected: total, Error: err}, err
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return &BatchResult{RowsAffected: total}, nil
}

// ExecuteSQLs batch-executes multiple SQL statements.
func (b *Batch) ExecuteSQLs(sqls []string) (*BatchResult, error) {
	pool, err := b.config.Pool()
	if err != nil {
		return nil, err
	}
	var total int64
	for _, sql := range sqls {
		result, err := pool.Exec(sql)
		if err != nil {
			return &BatchResult{RowsAffected: total, Error: err}, fmt.Errorf("sql error: %w", err)
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return &BatchResult{RowsAffected: total}, nil
}
