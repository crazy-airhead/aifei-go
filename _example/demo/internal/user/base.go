package user

import (
	"reflect"

	"github.com/crazy-airhead/aifei-go/db"
)

var Table = &db.Table{
	Name:        "user",
	Fields:      "id,name,age,email,created_at",
	PrimaryKeys: []string{"id"},
	FieldTypes: map[string]reflect.Type{

		"id": reflect.TypeOf(int(0)),

		"name": reflect.TypeOf(""),

		"age": reflect.TypeOf(int(0)),

		"email": reflect.TypeOf(""),

		"created_at": reflect.TypeOf(""),
	},
}

type BaseUser struct {
	*db.Row
}

func NewBase() *BaseUser {
	return &BaseUser{Row: db.NewRow(Table.Name)}
}

func NewWithRow(row *db.Row) *BaseUser {
	return &BaseUser{Row: row}
}

func (r *BaseUser) Id() int {
	return r.GetInt("id")
}

func (r *BaseUser) Name() string {
	return r.GetStr("name")
}

func (r *BaseUser) Age() int {
	return r.GetInt("age")
}

func (r *BaseUser) Email() string {
	return r.GetStr("email")
}

func (r *BaseUser) CreatedAt() string {
	return r.GetStr("created_at")
}

func (r *BaseUser) SetId(v int) *BaseUser {
	r.Set("id", v)
	return r
}

func (r *BaseUser) SetName(v string) *BaseUser {
	r.Set("name", v)
	return r
}

func (r *BaseUser) SetAge(v int) *BaseUser {
	r.Set("age", v)
	return r
}

func (r *BaseUser) SetEmail(v string) *BaseUser {
	r.Set("email", v)
	return r
}

func (r *BaseUser) SetCreatedAt(v string) *BaseUser {
	r.Set("created_at", v)
	return r
}

func (r *BaseUser) Id_(v int) *BaseUser {
	return r.SetId(v)
}

func (r *BaseUser) Name_(v string) *BaseUser {
	return r.SetName(v)
}

func (r *BaseUser) Age_(v int) *BaseUser {
	return r.SetAge(v)
}

func (r *BaseUser) Email_(v string) *BaseUser {
	return r.SetEmail(v)
}

func (r *BaseUser) CreatedAt_(v string) *BaseUser {
	return r.SetCreatedAt(v)
}

func (r *BaseUser) Insert() (*BaseUser, error) {
	_, err := r.Row.Insert()
	return r, err
}

func (r *BaseUser) Update() (bool, error) {
	return r.Row.Update()
}

func (r *BaseUser) Delete() (bool, error) {
	return r.Row.Delete()
}

func initRow(row *db.Row) *db.Row {
	return row.SetTable(Table.Name).SetPrimaryKeys(Table.PrimaryKeys...)
}

func init() {
	db.RegisterTable(Table)
}
