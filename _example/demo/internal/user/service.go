package user

import (
	"strconv"

	"github.com/crazy-airhead/aifei-go"
	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/server"
)

const ServicePrefix = "/user"

func init() {
	server.RegisterService(ServicePrefix, &Service{})
}

type Service struct{}

// List returns a paginated list.
func (s *Service) List(in aifei.Input) aifei.Output {
	pageNum := in.GetIntDefault("page", 1)
	pageSize := in.GetIntDefault("size", 10)
	page, err := db.SQL("SELECT * FROM user ORDER BY id DESC").Paginate(pageNum, pageSize)
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
		return server.Fail("User not found")
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
		return server.Fail("User not found")
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
