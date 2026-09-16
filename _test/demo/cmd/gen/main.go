// Generation tool for the demo.
// Usage: go run ./cmd/gen
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/tools/generator"

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
	db.RawSql(`CREATE TABLE IF NOT EXISTS user (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		age INTEGER DEFAULT 0,
		email TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`).Update()

	db.RawSql(`CREATE TABLE IF NOT EXISTS sys_login_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		login_time DATETIME DEFAULT CURRENT_TIMESTAMP,
		ip TEXT
	)`).Update()

	// flow 领域的两张表 → 同一个 flow 包（包 = 领域，Qualified 命名）
	db.RawSql(`CREATE TABLE IF NOT EXISTS sys_flow_task (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		state TEXT DEFAULT 'pending',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`).Update()

	db.RawSql(`CREATE TABLE IF NOT EXISTS sys_flow_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		action TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`).Update()

	pool, err := db.GetConfig().Pool()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get pool: %v\n", err)
		os.Exit(1)
	}

	dialect := &generator.SQLiteMetaDialect{}

	// Generate code into ./internal
	outputDir, _ := filepath.Abs("./internal")
	importRoot := "github.com/crazy-airhead/aifei-go/_test/demo/internal"

	util := &generator.TemplateUtil{}
	gen := generator.New(pool, dialect, outputDir, importRoot)
	gen.TablePrefix = "sys_"

	// Qualified 模式：包级标识符带表名（TableUser / NewUserDao / FlowTaskFindById...），
	// 文件名 base_<table>.go。flow_task 与 flow_log 映射进同一个 flow 包，
	// 演示「包 = 领域」的多表一包。
	gen.Qualified = true
	gen.PkgNameFunc = func(tableName string) string {
		if strings.HasPrefix(tableName, "flow_") {
			return "flow"
		}
		return util.PkgName(tableName)
	}
	if err := gen.Generate(); err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Code generation complete. Run 'go run .' to start the demo.")
}
