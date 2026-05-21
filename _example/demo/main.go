package main

import (
	"fmt"

	"github.com/aifei/aifei"
	"github.com/aifei/aifei/db"
)

func main() {
	// Init database
	err := db.Init("sqlite", "./demo.db", db.WithPrinter(func(sql string, args ...interface{}) {
		fmt.Printf("[SQL] %s %v\n", sql, args)
	}))
	if err != nil {
		panic(err)
	}

	// Create table
	db.SQL("CREATE TABLE IF NOT EXISTS user (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, age INTEGER, email TEXT, created_at TEXT)").Update()

	app := aifei.New()

	// Global middleware
	app.Use(aifei.Logger(), aifei.Recover(), aifei.CORS("*"))

	// Root
	app.GET("/", func(c *aifei.Context) {
		c.Text("Aifei Go %s", aifei.Version)
	})

	// RESTful routes
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

// ---- Handlers ----

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
	id := c.Param("id")
	row, err := db.FindByID("user", id)
	if err != nil || row == nil {
		c.JsonFail("user not found")
		return
	}
	c.JsonOK(row)
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
		row := db.NewRow("user").ID(input.ID).Set("name", input.Name).Set("age", input.Age).Set("email", input.Email)
		_, err := db.Update(row)
		if err != nil {
			c.JsonFail(err.Error())
			return
		}
		c.JsonOK(input.ID)
	} else {
		row := db.NewRow("user").Set("name", input.Name).Set("age", input.Age).Set("email", input.Email)
		result, err := db.Insert(row)
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
	_, err := db.DeleteByID("user", input.ID)
	if err != nil {
		c.JsonFail(err.Error())
		return
	}
	c.JsonOK(nil)
}

func UserCount(c *aifei.Context) {
	count, err := db.Count("user")
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
