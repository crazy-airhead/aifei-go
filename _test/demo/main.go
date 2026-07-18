package main

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/server"

	// Per-table package: registers Table metadata and Service routes via init().
	_ "github.com/crazy-airhead/aifei-go/_test/demo/internal/loginlog"
	_ "github.com/crazy-airhead/aifei-go/_test/demo/internal/user"

	_ "modernc.org/sqlite"
)

func main() {
	// Init database
	err := db.Init("sqlite", "./demo.db", db.WithPrinter(func(sql string, args ...interface{}) {
		fmt.Println(sql)
	}))
	if err != nil {
		panic(err)
	}

	// Ensure tables exist
	db.RawSql(`CREATE TABLE IF NOT EXISTS user (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			age INTEGER DEFAULT 0,
			email TEXT,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP
		)`).Update()

	db.RawSql(`CREATE TABLE IF NOT EXISTS sys_login_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			login_time DATETIME DEFAULT CURRENT_TIMESTAMP,
			ip TEXT
		)`).Update()

	app := aifei.New()

	// Global middleware (handler-level: Input → Output)
	app.Use(server.Logger(), server.Recover())

	// Root
	app.GET("/", func(in aifei.Input) aifei.Output {
		return server.Of("Aifei Go " + aifei.Version)
	})

	// Tables self-registered via init() in each per-table base.go.
	fmt.Printf("[DEMO] Registered tables: %d\n", len(db.Tables()))
	for _, t := range db.Tables() {
		fmt.Printf("[DEMO]   %s (pk=%v, fields=%s)\n", t.Name, t.PrimaryKeys, t.Fields)
	}

	// Auto-register all services that self-registered via init().
	server.AutoRegisterServices(app)
	fmt.Printf("[DEMO] Registered services: %d\n", len(server.ServiceRegistrations()))

	// Custom route group with auth (HTTP-level middleware)
	admin := app.Group("/api/admin")
	admin.GET("/dashboard", func(in aifei.Input) aifei.Output {
		return server.Of("admin dashboard")
	})

	// Start with HTTP-level middleware
	server.Run(app, ":8081",
		server.WithCORS("*"),
		//server.WithBasicAuth(func(u, p string) bool {
		//	// Note: This protects ALL routes. To protect only /api/admin,
		//	// use path-aware middleware or a separate server instance.
		//	return true // allow all for demo
		//}),
	)
}
