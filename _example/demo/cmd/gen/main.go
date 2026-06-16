// Generation tool for the demo.
// Usage: go run ./cmd/gen
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/generator"

	_ "modernc.org/sqlite"
)

func main() {
	// Init database
	err := db.Init("sqlite", "./demo.db")
	if err != nil {
		fmt.Fprintf(os.Stderr, "db init: %v\n", err)
		os.Exit(1)
	}

	// Ensure tables exist
	db.SQL(`CREATE TABLE IF NOT EXISTS user (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		age INTEGER DEFAULT 0,
		email TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`).Update()

	db.SQL(`CREATE TABLE IF NOT EXISTS sys_login_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		login_time DATETIME DEFAULT CURRENT_TIMESTAMP,
		ip TEXT
	)`).Update()

	pool, err := db.GetConfig().Pool()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get pool: %v\n", err)
		os.Exit(1)
	}

	dialect := &generator.SQLiteMetaDialect{}

	// Generate code into ./internal
	outputDir, _ := filepath.Abs("./internal")
	importRoot := "github.com/crazy-airhead/aifei-go/_example/demo/internal"

	gen := generator.New(pool, dialect, outputDir, importRoot)
	gen.TablePrefix = "sys_"
	if err := gen.Generate(); err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Code generation complete. Run 'go run .' to start the demo.")
}
