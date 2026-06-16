package loginlog

import (
	"strconv"

	"github.com/crazy-airhead/aifei-go"
	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/server"
)

const (
	ServicePrefix = "/loginLog"

	// listSql is the Enjoy SQL template for List and Paginate.
	// Query params are matched via
	listSql = `SELECT * FROM sys_login_log
#where(user_id, '=', user_id)
#and(login_time, '=', login_time)
#and(ip, '=', ip)
ORDER BY id DESC`
)

func init() {
	server.RegisterService(ServicePrefix, &Service{})
}

type Service struct{}

// List returns records matching query params (no pagination).
func (s *Service) List(in aifei.Input) aifei.Output {
	filter := in.GetMap()
	rows, err := db.Sql(listSql, filter).Find()
	if err != nil {
		return server.Fail(err.Error())
	}
	return server.Of(rows)
}

// Paginate returns paginated records matching query params.
func (s *Service) Paginate(in aifei.Input) aifei.Output {
	pageNum := in.GetIntDefault("page", 1)
	pageSize := in.GetIntDefault("size", 10)
	filter := in.GetMap()
	page, err := db.Sql(listSql, filter).Paginate(pageNum, pageSize)
	if err != nil {
		return server.Fail(err.Error())
	}
	return server.Of(page)
}

// Create inserts a new record.
func (s *Service) Create(in aifei.Input) aifei.Output {
	u := New()
	if err := in.GetBean(u); err != nil {
		return server.Fail("invalid request: " + err.Error())
	}
	result, err := u.Insert()
	if err != nil {
		return server.Fail(err.Error())
	}
	return server.Of(result.GetID())
}

// GetById retrieves a record by primary key.
func (s *Service) GetById(in aifei.Input) aifei.Output {
	id, err := strconv.Atoi(in.Param("id"))
	if err != nil {
		return server.Fail("invalid id")
	}
	u, err := FindById(id)
	if err != nil {
		return server.Fail(err.Error())
	}
	if u == nil {
		return server.Fail("LoginLog not found")
	}
	return server.Of(u)
}

// UpdateById updates a record by primary key.
func (s *Service) UpdateById(in aifei.Input) aifei.Output {
	id, err := strconv.Atoi(in.Param("id"))
	if err != nil {
		return server.Fail("invalid id")
	}
	existing, err := FindById(id)
	if err != nil || existing == nil {
		return server.Fail("LoginLog not found")
	}
	if err := in.GetBean(existing); err != nil {
		return server.Fail("invalid request: " + err.Error())
	}
	if _, err := existing.Update(); err != nil {
		return server.Fail(err.Error())
	}
	return server.Of(id)
}

// DeleteById deletes a record by primary key.
func (s *Service) DeleteById(in aifei.Input) aifei.Output {
	id, err := strconv.Atoi(in.Param("id"))
	if err != nil {
		return server.Fail("invalid id")
	}
	if _, err := DeleteById(id); err != nil {
		return server.Fail(err.Error())
	}
	return server.Of(nil)
}
