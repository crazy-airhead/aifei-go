package main

import (
	"fmt"
	"strconv"

	"github.com/crazy-airhead/aifei-go"
	"github.com/crazy-airhead/aifei-go/_example/demo/internal/model/user"
	"github.com/crazy-airhead/aifei-go/db"

	_ "modernc.org/sqlite"
)

func main() {
	// Init database
	err := db.Init("sqlite", "./demo.db", db.WithPrinter(func(sql string, args ...interface{}) {
		fmt.Printf("[SQL] %s %v\n", sql, args)
	}))
	if err != nil {
		panic(err)
	}

	// Ensure table exists
	db.SQL(`CREATE TABLE IF NOT EXISTS user (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		age INTEGER DEFAULT 0,
		email TEXT,
		created_at TEXT DEFAULT CURRENT_TIMESTAMP
	)`).Update()

	app := aifei.New()

	// Global middleware
	app.Use(aifei.Logger(), aifei.Recover(), aifei.CORS("*"))

	// Root
	app.GET("/", func(c *aifei.Context) {
		c.Text("Aifei Go %s", aifei.Version)
	})

	// RESTful routes using generated code
	app.GET("/api/user/list", UserList)
	app.GET("/api/user/:id", UserGet)
	app.POST("/api/user/save", UserSave)
	app.POST("/api/user/delete", UserDelete)
	app.GET("/api/user/count", UserCount)

	// Struct registration (Just Service)
	app.Register("/api/v2/user", &UserService{})

	// Route group with auth
	admin := app.Group("/api/admin", aifei.BasicAuth(func(u, p string) bool {
		return u == "admin" && p == "123456"
	}))
	admin.GET("/dashboard", func(c *aifei.Context) {
		c.JsonOK("admin dashboard")
	})

	// Start
	app.Run(":8080")
}

// ---- Handlers using generated user package ----

func UserList(c *aifei.Context) {
	name := c.GetStr("name")
	pageNum := c.GetIntDefault("page", 1)
	pageSize := c.GetIntDefault("size", 10)

	builder := db.NewSQL("SELECT * FROM user")
	if name != "" {
		builder.Where("name LIKE ?", "%"+name+"%")
	}

	page, err := builder.OrderBy("id DESC").Paginate(pageNum, pageSize)
	if err != nil {
		c.JsonFail(err.Error())
		return
	}
	c.JsonOK(page)
}

func UserGet(c *aifei.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u, err := user.FindById(id)
	if err != nil || u == nil {
		c.JsonFail("user not found")
		return
	}
	c.JsonOK(map[string]interface{}{
		"id":   u.Id(),
		"name": u.Name(),
		"age":  u.Age(),
	})
}

func UserSave(c *aifei.Context) {
	var input struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Age   int    `json:"age"`
		Email string `json:"email"`
	}
	if err := c.GetBean(&input); err != nil {
		c.JsonFail("invalid request: " + err.Error())
		return
	}

	if input.ID > 0 {
		// Update using generated Row
		u, err := user.FindById(int(input.ID))
		if err != nil || u == nil {
			c.JsonFail("user not found")
			return
		}
		u.SetName(input.Name).SetAge(input.Age).SetEmail(input.Email)
		if _, err := u.Update(); err != nil {
			c.JsonFail(err.Error())
			return
		}
		c.JsonOK(input.ID)
	} else {
		// Insert using generated short setters
		u := user.New().Name_(input.Name).Age_(input.Age).Email_(input.Email)
		result, err := u.Insert()
		if err != nil {
			c.JsonFail(err.Error())
			return
		}
		c.JsonOK(result.GetID())
	}
}

func UserDelete(c *aifei.Context) {
	var input struct {
		ID int64 `json:"id"`
	}
	if err := c.GetBean(&input); err != nil {
		c.JsonFail("invalid request")
		return
	}
	if _, err := user.DeleteById(int(input.ID)); err != nil {
		c.JsonFail(err.Error())
		return
	}
	c.JsonOK(nil)
}

func UserCount(c *aifei.Context) {
	count, err := user.Count()
	if err != nil {
		c.JsonFail(err.Error())
		return
	}
	c.JsonOK(count)
}

// ---- Struct Registration (Just Service) ----

type UserService struct{}

func (s *UserService) List(c *aifei.Context) { UserList(c) }
func (s *UserService) Save(c *aifei.Context) { UserSave(c) }
