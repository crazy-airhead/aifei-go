package user

import (
	"github.com/crazy-airhead/aifei-go/db"
)

type Dao struct {
	*db.Dao
}

func NewDao() *Dao {
	return &Dao{Dao: db.Use()}
}

func FindById(id int) (*User, error) {
	row, err := db.FindByID(Table.Name, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return &User{BaseUser: NewWithRow(initRow(row))}, nil
}

func DeleteById(id int) (bool, error) {
	return db.DeleteByID(Table.Name, id)
}

func FindBy(whereOrField string, args ...interface{}) ([]*User, error) {
	rows, err := db.FindBy(Table.Name, whereOrField, args...)
	if err != nil {
		return nil, err
	}
	result := make([]*User, len(rows))
	for i, row := range rows {
		result[i] = &User{BaseUser: NewWithRow(initRow(row))}
	}
	return result, nil
}

func FindFirstBy(whereOrField string, args ...interface{}) (*User, error) {
	row, err := db.FindFirstBy(Table.Name, whereOrField, args...)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return &User{BaseUser: NewWithRow(initRow(row))}, nil
}

func DeleteBy(whereOrField string, args ...interface{}) (int64, error) {
	return db.DeleteBy(Table.Name, whereOrField, args...)
}

func Count() (int64, error) {
	return db.Count(Table.Name)
}

func CountBy(whereOrField string, args ...interface{}) (int64, error) {
	return db.CountBy(Table.Name, whereOrField, args...)
}
