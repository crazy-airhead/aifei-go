# Phase 3 实施提示词: 数据库模块

## 使用说明

在完成 Phase 1 (核心框架) 和 Phase 2 (Enjoy 模板引擎) 后，在 db/ 子目录中依次执行以下提示词。
注意: DB 模块依赖 Enjoy 引擎来实现 Enjoy SQL (#para, #where, #and, #orderBy 指令)。

---

## Prompt 3.1: 数据库配置 + Dialect

```
在 aifei 框架的 db/ 子包中创建数据库配置和方言:

1. db/config.go — 数据库配置:
   Config 结构体:
   - ID string — 配置标识 (默认 "main")
   - DriverName string — 驱动名: mysql, postgres, sqlite
   - DSN string — 数据源连接串
   - Dialect Dialect — 数据库方言
   - MaxOpen int — 最大打开连接数
   - MaxIdle int — 最大空闲连接数
   - MaxLife time.Duration — 连接最大生命周期
   - Printer func(sql string, args ...interface{}) — SQL 日志打印
   - pool *sql.DB — 连接池 (懒初始化)

   ConfigOption 类型: type ConfigOption func(*Config)
   - WithDialect(d Dialect) ConfigOption
   - WithMaxOpen(n int) ConfigOption
   - WithMaxIdle(n int) ConfigOption
   - WithMaxLife(d time.Duration) ConfigOption
   - WithPrinter(fn func(string, ...interface{})) ConfigOption

   方法:
   - Pool() (*sql.DB, error) — 获取连接池 (懒初始化)
   - CreateDao() *Dao — 创建 Dao 实例
   - CreateBatch() *Batch — 创建 Batch 实例
   - GetDialect() Dialect — 获取方言 (未设置时根据 DriverName 自动推断)

   全局配置管理:
   - var configs = map[string]*Config{}
   - Init(driverName, dsn string, opts ...ConfigOption) error — 初始化默认数据库
   - InitWithID(configID, driverName, dsn string, opts ...ConfigOption) error
   - GetConfig(id ...string) *Config — 获取配置 (默认 "main")

2. db/dialect.go — 方言接口:
   type Dialect interface {
       Name() string
       DefaultPrimaryKeys() []string
       ForFindByID(table string, pks []string) string
       ForDeleteByID(table string, pks []string) string
       ForInsert(table string, fields []string) string
       ForUpdate(table string, fields []string, pks []string) string
       ForInsertOrUpdate(table string, fields []string, pks []string) string
       ForPaginate(selectSQL, fromWhereSQL string, pageNum, pageSize int) string
   }

   辅助函数:
   - func NewDialect(driverName string) Dialect — 根据 driverName 自动创建方言

3. db/dialect_mysql.go — MySQL 方言:
   - LIMIT offset, count 分页
   - INSERT ... ON DUPLICATE KEY UPDATE 实现 insertOrUpdate
   - 默认主键 ["id"]
   - 反引号 `` 包裹表名和字段名

4. db/dialect_postgres.go — PostgreSQL 方言:
   - LIMIT count OFFSET offset 分页
   - INSERT ... ON CONFLICT DO UPDATE 实现 insertOrUpdate
   - 默认主键 ["id"]
   - 双引号包裹

5. db/dialect_sqlite.go — SQLite 方言:
   - LIMIT count OFFSET offset 分页
   - INSERT OR REPLACE 实现 insertOrUpdate
   - 默认主键 ["id"]

注意:
- 使用 database/sql 标准库
- Pool() 方法懒初始化，首次调用时 sql.Open
- Dialect 自动推断: "mysql" → MySQLDialect, "postgres"/"pgx" → PostgresDialect, "sqlite" → SQLiteDialect
```

---

## Prompt 3.2: Row + TypeConverter

```
在 aifei 框架的 db/ 子包中创建 Row 和类型转换器:

1. db/type_converter.go — 类型转换器:
   func ToInt(v interface{}) int         — 支持 int/int8/int16/int32/int64/float64/string
   func ToInt64(v interface{}) int64
   func ToFloat64(v interface{}) float64
   func ToBool(v interface{}) bool       — 支持 bool/int/string("true"/"1"/"yes")
   func ToString(v interface{}) string
   func ToTime(v interface{}) time.Time  — 支持 time.Time/string(常用格式)
   所有函数遇到 nil 或无法转换时返回零值

2. db/row.go — Row 数据行:
   Row 结构体:
   - table string — 表名
   - primaryKeys []string — 主键名 (默认 ["id"])
   - data map[string]interface{} — 数据
   - change map[string]struct{} — 追踪 set 操作的字段名

   工厂方法:
   - NewRow(table string) *Row — 默认主键 "id"
   - NewRowWithPK(table, pk string) *Row — 指定主键
   - NewRowWithCompositePK(table, pk1, pk2 string) *Row — 复合主键

   表/主键:
   - Table() string
   - SetTable(table string) *Row
   - PrimaryKeys() []string
   - SetPrimaryKeys(pks ...string) *Row
   - ID(id interface{}) *Row — 设置主键值
   - GetID() interface{} — 获取主键值
   - CompositeID(id1, id2 interface{}) *Row — 设置复合主键值

   Set 系列 (追踪 change):
   - Set(field string, value interface{}) *Row
   - SetMap(data map[string]interface{}) *Row
   - SetIfNotNull(field string, value interface{}) *Row
   - SetIfNotBlank(field string, value string) *Row

   Put 系列 (不追踪 change):
   - Put(field string, value interface{}) *Row
   - PutMap(data map[string]interface{}) *Row

   移除:
   - Remove(fields ...string) *Row
   - RemoveNullFields() *Row
   - Keep(fields ...string) *Row
   - Clear() *Row

   判断:
   - Has(field string) bool
   - Size() int

   类型安全 Getter (使用 TypeConverter):
   - GetStr(field string) string / GetStrDefault(field, def string) string
   - GetInt(field string) int / GetIntDefault(field string, def int) int
   - GetInt64(field string) int64 / GetInt64Default(field string, def int64) int64
   - GetFloat64(field string) float64 / GetFloat64Default(field string, def float64) float64
   - GetBool(field string) bool / GetBoolDefault(field string, def bool) bool
   - GetTime(field string) time.Time / GetTimeDefault(field string, def time.Time) time.Time
   - GetBytes(field string) []byte
   - Get(field string) interface{} / GetDefault(field string, def interface{}) interface{}

   其他:
   - FieldNames() []string
   - FieldValues() []interface{}
   - ForEach(fn func(key string, value interface{}))
   - ChangeSet() map[string]struct{} — 获取 change 集合
   - ClearChange()
   - ChangedFields() []string — 获取 change 集合中的字段名列表

   JSON 支持:
   - MarshalJSON() ([]byte, error) — 序列化 data
   - UnmarshalJSON(data []byte) error — 反序列化到 data

   Active Record 操作 (调用 db 包级函数):
   - Insert() (*Row, error)
   - InsertOrUpdate() (*Row, error)
   - Update() (bool, error)
   - Delete() (bool, error)

注意:
- data map 懒初始化 (首次访问时创建)
- change map 懒初始化
- Active Record 方法调用 db 全局函数 (Use().InsertRow(this) 等)
```

---

## Prompt 3.3: Dao + Page

```
在 aifei 框架的 db/ 子包中创建 Dao 和 Page:

1. db/page.go — 分页:
   Page 结构体 (带 json tag):
   - PageNum int    `json:"pageNum"`
   - PageSize int   `json:"pageSize"`
   - TotalRows int64 `json:"totalRows"`
   - TotalPages int `json:"totalPages"`
   - Rows []*Row    `json:"rows"`

   NewPage(pageNum, pageSize int, totalRows int64, rows []*Row) *Page
   - TotalPages 计算: totalRows % pageSize != 0 ? totalRows/pageSize + 1 : totalRows/pageSize

   方法:
   - IsFirstPage() bool    — pageNum == 1
   - IsLastPage() bool     — pageNum >= totalPages
   - HasPreviousPage() bool — pageNum > 1
   - HasNextPage() bool    — pageNum < totalPages

2. db/dao.go — Dao 链式调用:
   Dao 结构体 (非线程安全):
   - config *Config
   - sqlStr string — SQL 语句
   - sqlArgs []interface{} — SQL 参数
   - selFields string — select 指定字段
   - fromTable string — from 指定表名

   链式入口:
   - SQL(query string, args ...interface{}) *Dao — 设置 SQL + 参数
   - SQLByID(sqlID string, args ...interface{}) *Dao — 通过 ID 获取 SQL (简化版: 暂不实现，预留接口)
   - Select(fields string) *Dao — 指定返回字段

   链式终止 (查询):
   - Find() ([]*Row, error) — 查询多行
   - FindFirst() (*Row, error) — 查询第一行
   - Paginate(pageNum, pageSize int) (*Page, error) — 分页查询
     实现: 先执行 COUNT 查询获取 totalRows，再执行带 LIMIT/OFFSET 的查询

   链式终止 (更新):
   - Update() (int64, error) — 执行 update/delete/insert，返回影响行数

   CRUD 操作:
   - InsertRow(row *Row) (*Row, error) — 插入行
     实现: 使用 Dialect.ForInsert 生成 SQL，执行后获取自增 ID 填入 row
   - InsertOrUpdateRow(row *Row) (*Row, error)
   - UpdateRow(row *Row) (bool, error) — 使用 change 集合更新
     实现: 使用 Dialect.ForUpdate 生成 SQL，只更新 change 集合中的字段
   - DeleteRow(row *Row) (bool, error) — 根据主键删除

   Table 操作 (带表名):
   - FindByID(table string, id interface{}) (*Row, error)
   - FindByIDWithPK(table, pk string, id interface{}) (*Row, error)
   - FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error)
     whereOrField 判断: 包含空格 → 当作 where 条件; 不包含 → 当作字段名 = ?
   - FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error)
   - FindAll(table string) ([]*Row, error)
   - DeleteByID(table string, id interface{}) (bool, error)
   - DeleteByIDWithPK(table, pk string, id interface{}) (bool, error)
   - DeleteBy(table, whereOrField string, args ...interface{}) (int64, error)
   - DeleteIn(table, field string, values ...interface{}) (int64, error)
   - FindIn(table, field string, values ...interface{}) ([]*Row, error)
   - Count(table string) (int64, error)
   - CountBy(table, whereOrField string, args ...interface{}) (int64, error)

注意:
- FindByID 内部调用 config.FindExecutor 或直接实现
- Paginate 的 COUNT 查询: 将 SELECT ... FROM 替换为 SELECT COUNT(*) FROM (去掉 ORDER BY)
- FindBy 的 whereOrField 判断逻辑: strings.Contains(whereOrField, " ") 则作为完整 where
- 所有错误通过 error 返回，不 panic
```

---

## Prompt 3.4: Db 入口 + Batch + Transaction + Operator

```
在 aifei 框架的 db/ 子包中创建 Db 入口、批量操作、事务、操作符:

1. db/db.go — Db 全局入口:
   全局函数 (直接转发到 Use()):
   - Use() *Dao — 获取默认 Dao
   - UseWithID(configID string) *Dao
   - SQL(query string, args ...interface{}) *Dao
   - Select(fields string) *Dao
   - Insert(row *Row) (*Row, error)
   - InsertOrUpdate(row *Row) (*Row, error)
   - Update(row *Row) (bool, error)
   - Delete(row *Row) (bool, error)
   - DeleteByID(table string, id interface{}) (bool, error)
   - DeleteByIDWithPK(table, pk string, id interface{}) (bool, error)
   - DeleteBy(table, whereOrField string, args ...interface{}) (int64, error)
   - DeleteIn(table, field string, values ...interface{}) (int64, error)
   - FindByID(table string, id interface{}) (*Row, error)
   - FindByIDWithPK(table, pk string, id interface{}) (*Row, error)
   - FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error)
   - FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error)
   - FindIn(table, field string, values ...interface{}) ([]*Row, error)
   - Count(table string) (int64, error)
   - CountBy(table, whereOrField string, args ...interface{}) (int64, error)
   - Batch() *Batch — 获取批量操作对象
   - Transaction(fn func() error) error — 事务

2. db/batch.go — 批量操作:
   BatchResult 结构体: RowsAffected(int64), Error(error)
   Batch 结构体: config(*Config)

   方法:
   - Insert(rows []*Row) (*BatchResult, error)
   - InsertWithTable(table string, rows []*Row) (*BatchResult, error)
   - Update(rows []*Row) (*BatchResult, error)
   - UpdateWithTable(table string, rows []*Row) (*BatchResult, error)
   - Execute(sql string, argsList [][]interface{}) (*BatchResult, error)
   - ExecuteSQLs(sqls []string) (*BatchResult, error)

   实现: 使用 database/sql 的 Prepare + Exec 循环执行

3. db/transaction.go — 事务:
   - Transaction(fn func() error) error
   - TransactionWithID(configID string, fn func() error) error

   实现: sql.DB.Begin() → fn() → Commit/Rollback

4. db/operator.go — SQL 操作符:
   type Operator string
   const (
       OpEqual Operator = "="
       OpNotEqual Operator = "!="
       OpGreater Operator = ">"
       OpGreaterEqual Operator = ">="
       OpLess Operator = "<"
       OpLessEqual Operator = "<="
       OpIn Operator = "IN"
       OpNotIn Operator = "NOT IN"
       OpBetween Operator = "BETWEEN"
       OpNotBetween Operator = "NOT BETWEEN"
       OpIsNull Operator = "IS NULL"
       OpIsNotNull Operator = "IS NOT NULL"
       OpLike Operator = "LIKE"
       OpNotLike Operator = "NOT LIKE"
   )

   辅助函数:
   - LikeContains(value string) string — "%value%"
   - LikeStartsWith(value string) string — "value%"
   - LikeEndsWith(value string) string — "%value"

5. db/sql_builder.go — 条件 SQL 构建器 (简化版 Enjoy SQL):
   SQLBuilder 结构体:
   - selectPart, fromPart string
   - whereParts []string
   - whereArgs []interface{}
   - orderByPart string
   - limitVal, offsetVal int

   方法:
   - Where(condition string, args ...interface{}) *SQLBuilder
   - WhereIf(condition string, arg interface{}, apply bool) *SQLBuilder
   - And(condition string, args ...interface{}) *SQLBuilder — 等价于 Where
   - AndIf(condition string, arg interface{}, apply bool) *SQLBuilder
   - OrderBy(order string) *SQLBuilder
   - Limit(limit int) *SQLBuilder
   - Offset(offset int) *SQLBuilder
   - Build() (string, []interface{}) — 构建最终 SQL 和参数
   - Find() ([]*Row, error)
   - FindFirst() (*Row, error)
   - Paginate(pageNum, pageSize int) (*Page, error)
   - Count() (int64, error)

   全局入口:
   - NewSQL(sql string, args ...interface{}) *SQLBuilder — 从已有 SQL 创建

注意:
- db.go 中的全局函数都是 Use().XXX() 的快捷方式
- Transaction 中的 fn 如果返回 error != nil 则 Rollback
- SQLBuilder.Build() 拼接: selectPart + " " + where("WHERE" + AND连接) + orderBy + limit/offset
```

---

## 验证 Prompt

```
验证 aifei db 模块 Phase 2 是否完成:

编写测试文件 db/db_test.go，使用 SQLite 内存数据库测试:

1. 初始化: db.Init("sqlite", ":memory:")
2. 建表: db.SQL("CREATE TABLE user (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)").Update()
3. 测试插入: db.Insert(db.NewRow("user").Set("name","james").Set("age",18))
4. 测试查询: db.FindByID("user", 1) 验证 name="james", age=18
5. 测试更新: db.Update(db.NewRow("user").ID(1).Set("age",19)) 验证 age=19
6. 测试删除: db.DeleteByID("user", 1) 验证 FindByID 返回 nil
7. 测试 FindBy: 插入多条数据，db.FindBy("user","age > ?",18) 验证结果
8. 测试分页: 插入25条，db.SQL("select * from user").Paginate(1,10) 验证 totalRows=25, rows=10
9. 测试事务: db.Transaction 插入2条验证都能成功，中间返回 error 验证回滚
10. 测试 Count: db.Count("user") 验证数量
11. 测试 Row 链式: db.NewRow("user").Set("name","test").Set("age",20).Insert()
12. 测试 SQLBuilder: db.SQL("select * from user").Where("age > ?",18).OrderBy("id desc").Find()

运行测试: go test ./db/ -v
```
