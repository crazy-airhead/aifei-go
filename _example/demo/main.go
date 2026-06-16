package main

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go"
	"github.com/crazy-airhead/aifei-go/db"

	// Per-table package: registers Table metadata and Service routes via init().
	_ "github.com/crazy-airhead/aifei-go/_example/demo/internal/user"

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

	// Tables self-registered via init() in each per-table base.go.
	fmt.Printf("[DEMO] Registered tables: %d\n", len(db.Tables()))
	for _, t := range db.Tables() {
		fmt.Printf("[DEMO]   %s (pk=%v, fields=%s)\n", t.Name, t.PrimaryKeys, t.Fields)
	}

	// Auto-register all services that self-registered via init().
	// Generated service provides: List, GetById, Save, DeleteById
	app.AutoRegisterServices()
	fmt.Printf("[DEMO] Registered services: %d\n", len(aifei.ServiceRegistrations()))

	// Custom route group with auth
	admin := app.Group("/api/admin", aifei.BasicAuth(func(u, p string) bool {
		return u == "admin" && p == "123456"
	}))
	admin.GET("/dashboard", func(c *aifei.Context) {
		c.JsonOK("admin dashboard")
	})

	// Start
	app.Run(":8080")
}
