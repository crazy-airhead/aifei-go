# Phase 2: 数据库模块

> 目标：实现 Aifei Go 的数据库访问层，包含 Db、Row、Dao、Page、Dialect、Transaction

## 1. 模块结构

```
db/
├── db.go                # Db 入口 (静态方法)
├── dao.go               # Dao 链式调用对象
├── row.go               # Row 数据行 (Active Record)
├── page.go              # Page 分页结果
├── batch.go             # Batch 批量操作
├── sql_builder.go       # SQL 构建 (简化版 Enjoy SQL)
├── operator.go          # SQL 操作符
├── condition.go         # 条件构建
├── dialect.go           # Dialect 接口
├── dialect_mysql.go     # MySQL 方言
├── dialect_postgres.go  # PostgreSQL 方言
├── dialect_sqlite.go    # SQLite 方言
├── config.go            # 数据库配置
├── type_converter.go    # 类型转换器
└── transaction.go       # 事务管理
```

---

## 2. Db 入口 (对应 `cn.aifei.db.core.Db`)

**Java Db 类完整静态方法清单：**
```java
// 获取 Dao
Dao use()
Dao use(String configId)

// SQL 操作
Dao sql(String sql, Object... paras)
Dao sql(String sql)
Dao sql(String sql, Map<?, ?> data)
Dao sqlById(String sqlId, Object... paras)
Dao sqlById(String sqlId)
Dao sqlById(String sqlId, Map<?, ?> data)
Dao sqlPara(String sql, Object... paras)

// Select 指定字段
Dao select(String fields)

// 插入
Row insert(Row row)
Row insertOrUpdate(Row row)

// 删除
boolean delete(Row row)
boolean deleteById(String table, Object id)
boolean deleteById(String table, String primaryKey, Object id)
boolean deleteByCompositeId(String table, String key1, String key2, Object id1, Object id2)
boolean deleteByCompositeId(String table, String[] compositeId, Object[] idValues)
int deleteBy(String table, String whereOrField, Object... paraArray)
int deleteInIds(String table, Collection<Object> ids)
int deleteInIds(String table, Object... idValues)
int deleteIn(String table, String field, Collection<Object> fieldValues)
int deleteIn(String table, String field, Object... fieldValues)

// 更新
boolean update(Row row)

// 查询
Row findById(String table, Object id)
Row findById(String table, String primaryKey, Object id)
Row findByCompositeId(String table, String key1, String key2, Object id1, Object id2)
Row findByCompositeId(String table, String[] compositeId, Object[] idValues)
List<Row> findBy(String table, String whereOrField, Object... paraArray)
Row findFirstBy(String table, String whereOrField, Object... paraArray)
List<Row> findInIds(String table, Collection<Object> ids)
List<Row> findInIds(String table, Object... idValues)
List<Row> findIn(String table, String field, Collection<Object> fieldValues)
List<Row> findIn(String table, String field, Object... fieldValues)

// 聚合
long count(String table)
long countBy(String table, String whereOrField, Object... paraArray)

// 批量
Batch batch()
Batch batch(String configId)
BatchResult batchInsert(List<? extends AifeiRow> rowList)
BatchResult batchInsert(String table, List<? extends AifeiRow> rowList)
BatchResult batchUpdate(List<? extends AifeiRow> rowList)
BatchResult batchUpdate(String table, List<? extends AifeiRow> rowList)
BatchResult batchExecute(String sql, List<Object[]> parasList)
BatchResult batchExecute(String sql, Object[][] parasArray)
BatchResult batchExecute(List<String> sqlList)

// 底层 JDBC
<R> R call(JdbcFun<R> fun)

// 事务
<R> R transaction(Atom<R> atom)
```

**Go 版设计：**

```go
package db

import "database/sql"

// 全局配置管理
var configs = map[string]*Config{}
var defaultConfigID = "main"

// Init 初始化默认数据库
func Init(driverName, dsn string, opts ...ConfigOption) error

// InitWithID 初始化指定 configID 的数据库
func InitWithID(configID, driverName, dsn string, opts ...ConfigOption) error

// Use 获取默认 Dao
func Use() *Dao

// UseWithID 获取指定 configID 的 Dao
func UseWithID(configID string) *Dao

// ---- 快捷方法 (直接转发到 Use()) ----

// SQL 执行 (链式入口)
func SQL(query string, args ...interface{}) *Dao

// Select 指定返回字段
func Select(fields string) *Dao

// Insert
func Insert(row *Row) (*Row, error)
func InsertOrUpdate(row *Row) (*Row, error)

// Delete
func Delete(row *Row) (bool, error)
func DeleteByID(table string, id interface{}) (bool, error)
func DeleteByIDWithPK(table, primaryKey string, id interface{}) (bool, error)
func DeleteBy(table, whereOrField string, args ...interface{}) (int64, error)
func DeleteInIDs(table string, ids ...interface{}) (int64, error)
func DeleteIn(table, field string, values ...interface{}) (int64, error)

// Update
func Update(row *Row) (bool, error)

// Query
func FindByID(table string, id interface{}) (*Row, error)
func FindByIDWithPK(table, primaryKey string, id interface{}) (*Row, error)
func FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error)
func FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error)
func FindInIDs(table string, ids ...interface{}) ([]*Row, error)
func FindIn(table, field string, values ...interface{}) ([]*Row, error)

// Aggregate
func Count(table string) (int64, error)
func CountBy(table, whereOrField string, args ...interface{}) (int64, error)

// Transaction
func Transaction(fn func(tx *sql.Tx) error) error
```

---

## 3. Dao (对应 `cn.aifei.db.core.Dao` + `cn.aifei.db.core.AifeiDao`)

**Java AifeiDao 关键方法 (Dao 继承自 AifeiDao)：**
```java
// SQL 入口 (链式调用起点)
D sql(String sql, Object... paras)
D sql(String sql)
D sql(String sql, Map<?, ?> data)
D sqlById(String sqlId, Object... paras)
D sqlPara(String sql, Object... paras)
D select(String fields)

// 链式终止方法
List<R> find()
R findFirst()
R findOne()
int update()
Page<R> paginate(int pageNum, int pageSize)
<T> List<T> query()          // 不封装为 Row
<T> T queryFirst()

// 带表名的操作 (在 Dao 子类中定义)
boolean deleteById(String table, Object id)
List<R> findAll(String table)
R findById(String table, Object id)
List<R> findBy(String table, String whereOrField, Object... paraArray)
...
```

**Go 版设计：**

```go
package db

type Dao struct {
    config    *Config
    sqlStr    string
    sqlArgs   []interface{}
    sqlData   map[string]interface{}
    selFields string   // select 指定字段
    fromTable string   // from 指定表名
}

// ---- 链式入口方法 ----

func (d *Dao) SQL(query string, args ...interface{}) *Dao
func (d *Dao) SQLWithData(query string, data map[string]interface{}) *Dao
func (d *Dao) SQLByID(sqlID string, args ...interface{}) *Dao
func (d *Dao) Select(fields string) *Dao

// ---- 链式终止方法 (查询) ----

func (d *Dao) Find() ([]*Row, error)
func (d *Dao) FindFirst() (*Row, error)
func (d *Dao) FindOne() (*Row, error)
func (d *Dao) Paginate(pageNum, pageSize int) (*Page, error)

// ---- 链式终止方法 (更新) ----

func (d *Dao) Update() (int64, error)

// ---- 链式终止方法 (原始查询) ----

func (d *Dao) Query() (*sql.Rows, error)
func (d *Dao) QueryInt() (int, error)
func (d *Dao) QueryInt64() (int64, error)
func (d *Dao) QueryString() (string, error)

// ---- CRUD 操作 ----

func (d *Dao) Insert(row *Row) (*Row, error)
func (d *Dao) InsertOrUpdate(row *Row) (*Row, error)
func (d *Dao) UpdateRow(row *Row) (bool, error)
func (d *Dao) DeleteRow(row *Row) (bool, error)

// ---- Table 操作 (带表名) ----

func (d *Dao) FindByID(table string, id interface{}) (*Row, error)
func (d *Dao) FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error)
func (d *Dao) FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error)
func (d *Dao) Count(table string) (int64, error)
func (d *Dao) CountBy(table, whereOrField string, args ...interface{}) (int64, error)
func (d *Dao) DeleteByID(table string, id interface{}) (bool, error)
func (d *Dao) DeleteBy(table, whereOrField string, args ...interface{}) (int64, error)
```

---

## 4. Row (对应 `cn.aifei.db.core.Row` + `cn.aifei.db.core.AifeiRow`)

**Java AifeiRow 完整方法清单：**
```java
// 表/主键配置
R table(String table)
R table(String table, String primaryKey)
R table(String table, String primaryKey1, String primaryKey2)
String table()
R primaryKey(String primaryKey)
R primaryKey(String... primaryKeys)
String[] primaryKey()

// 数据操作
R data(Map<String, Object> data)
R data(AifeiRow<?> row)
Map<String, Object> data()
int size()
R clear()
boolean has(String field)

// set 系列 (带 columnDefined 检查 + change 追踪)
R set(String field, Object value)
R set(Map<String, Object> data)
R set(AifeiRow<?> row)
R setOrPut(String field, Object value)
R setIfNotNull(String field, Object value)
R setIfNotBlank(String field, String value)

// put 系列 (不检查 columnDefined，不追踪 change)
R put(String key, Object value)
R put(AifeiRow<?> row)
R put(Map<String, Object> data)

// 移除
R remove(String... fields)
R removeNullValueFields()
R keep(String... fields)

// 类型安全 getter
String getStr(String field) / getStr(field, default)
Integer getInt(String field) / getInt(field, default)
Long getLong(String field) / getLong(field, default)
Double getDouble(String field)
Float getFloat(String field)
Boolean getBoolean(String field) / getBoolean(field, default)
BigDecimal getBigDecimal(field) / getBigDecimal(field, default)
BigInteger getBigInteger(field)
Date getDate(field) / getDate(field, default)
LocalDateTime getLocalDateTime(field) / getLocalDateTime(field, default)
Timestamp getTimestamp(field)
Time getTime(field)
Byte getByte(field)
byte[] getBytes(field)
Number getNumber(field)
<T> T get(field)
<T> T get(field, T defaultValue)
<T> T get(field, Function<Object, T> converter)
<T> T get(field, T defaultValue, Function<Object, T> converter)

// 其他
List<String> fieldNames()
List<Object> fieldValues()
Class<?> fieldType(String field)
boolean columnDefined(String field)
Set<String> change()
void clearChange()
<T extends AifeiRow<T>> T to(Class<T> modelClass)

// Row 子类特有方法
static Row of(String table)
static Row of(String table, String primaryKey)
static Row of(String table, String primaryKey1, String primaryKey2)
Row id(Object id)
<T> T id()
Row compositeId(Object id1, Object id2)
Row insert()
Row insertOrUpdate()
boolean delete()
boolean update()
```

**Go 版设计：**

```go
package db

type Row struct {
    table      string
    primaryKeys []string          // 默认 ["id"]
    data       map[string]interface{}
    change     map[string]struct{} // 追踪 set 操作的字段
}

// ---- 工厂方法 ----

func NewRow(table string) *Row
func NewRowWithPK(table, primaryKey string) *Row
func NewRowWithCompositePK(table, pk1, pk2 string) *Row

// ---- 表/主键 ----

func (r *Row) Table() string
func (r *Row) SetTable(table string) *Row
func (r *Row) PrimaryKeys() []string
func (r *Row) SetPrimaryKeys(pks ...string) *Row
func (r *Row) ID(id interface{}) *Row
func (r *Row) GetID() interface{}
func (r *Row) CompositeID(id1, id2 interface{}) *Row

// ---- Set 系列 (追踪 change) ----

func (r *Row) Set(field string, value interface{}) *Row
func (r *Row) SetMap(data map[string]interface{}) *Row
func (r *Row) SetIfNotNull(field string, value interface{}) *Row
func (r *Row) SetIfNotBlank(field string, value string) *Row

// ---- Put 系列 (不追踪 change) ----

func (r *Row) Put(field string, value interface{}) *Row
func (r *Row) PutMap(data map[string]interface{}) *Row

// ---- 移除 ----

func (r *Row) Remove(fields ...string) *Row
func (r *Row) RemoveNullFields() *Row
func (r *Row) Keep(fields ...string) *Row
func (r *Row) Clear() *Row

// ---- 类型安全 Getter ----

func (r *Row) Has(field string) bool
func (r *Row) Size() int
func (r *Row) Get(field string) interface{}
func (r *Row) GetDefault(field string, def interface{}) interface{}
func (r *Row) GetStr(field string) string
func (r *Row) GetStrDefault(field, def string) string
func (r *Row) GetInt(field string) int
func (r *Row) GetIntDefault(field string, def int) int
func (r *Row) GetInt64(field string) int64
func (r *Row) GetInt64Default(field string, def int64) int64
func (r *Row) GetFloat64(field string) float64
func (r *Row) GetFloat64Default(field string, def float64) float64
func (r *Row) GetBool(field string) bool
func (r *Row) GetBoolDefault(field string, def bool) bool
func (r *Row) GetTime(field string) time.Time
func (r *Row) GetTimeDefault(field string, def time.Time) time.Time
func (r *Row) GetBytes(field string) []byte

// ---- 迭代 ----

func (r *Row) FieldNames() []string
func (r *Row) FieldValues() []interface{}
func (r *Row) ForEach(fn func(key string, value interface{}))
func (r *Row) ChangeSet() map[string]struct{}
func (r *Row) ClearChange()

// ---- Active Record 操作 (直接操作数据库) ----

func (r *Row) Insert() (*Row, error)
func (r *Row) InsertOrUpdate() (*Row, error)
func (r *Row) Update() (bool, error)
func (r *Row) Delete() (bool, error)
```

---

## 5. Page (对应 `cn.aifei.db.core.Page`)

**Java 版：**
```java
public class Page<T> {
    int pageNum, pageSize;
    long totalRows;
    int totalPages;
    List<T> rows;

    boolean isFirstPage()    // pageNum == 1
    boolean isLastPage()     // pageNum >= totalPages
    boolean hasPreviousPage() // pageNum > 1
    boolean hasNextPage()    // pageNum < totalPages
}
```

**Go 版：**

```go
package db

type Page struct {
    PageNum    int       `json:"pageNum"`
    PageSize   int       `json:"pageSize"`
    TotalRows  int64     `json:"totalRows"`
    TotalPages int       `json:"totalPages"`
    Rows       []*Row    `json:"rows"`
}

func NewPage(pageNum, pageSize int, totalRows int64, rows []*Row) *Page

func (p *Page) IsFirstPage() bool
func (p *Page) IsLastPage() bool
func (p *Page) HasPreviousPage() bool
func (p *Page) HasNextPage() bool
```

---

## 6. Dialect (对应 `cn.aifei.db.dialect.Dialect` + 各实现)

**Java Dialect 接口关键方法：**
```java
String forPaginate(String select, String sqlExceptSelect, int pageNum, int pageSize)
String forFindById(String table, String primaryKey)
String forDeleteById(String table, String primaryKey)
String forUpdate(String table, Map<String, Object> attrs, String primaryKey)
String forInsert(String table, Map<String, Object> attrs)
String[] getDefaultPrimaryKey()
```

**Go 版：**

```go
package db

type Dialect interface {
    Name() string
    DefaultPrimaryKeys() []string
    ForPaginate(selectSQL, fromWhereSQL string, pageNum, pageSize int) string
    ForFindByID(table string, primaryKeys []string) string
    ForDeleteByID(table string, primaryKeys []string) string
    ForInsert(table string, fields []string) string
    ForUpdate(table string, fields []string, primaryKeys []string) string
    ForInsertOrUpdate(table string, fields []string, primaryKeys []string) string
}

// MySQL
type MySQLDialect struct{}
func (d *MySQLDialect) ForPaginate(selectSQL, fromWhereSQL string, pageNum, pageSize int) string {
    offset := (pageNum - 1) * pageSize
    return fmt.Sprintf("%s %s LIMIT %d OFFSET %d", selectSQL, fromWhereSQL, pageSize, offset)
}

// PostgreSQL
type PostgresDialect struct{}

// SQLite
type SQLiteDialect struct{}
```

---

## 7. SQL 构建 (简化版 Enjoy SQL)

**Java 版 Enjoy SQL 指令：**
- `#para(int)` — 问号占位符参数
- `#para(name)` — 命名参数
- `#where()` — 动态 where
- `#and()` — 动态 and
- `#orderBy()` — 动态排序
- `#sql(id) ... #end` — SQL 片段定义
- `#@id()` — SQL 片段引用
- `#include(file)` — 包含文件

**Go 版 — 简化为条件 SQL 构建器：**

```go
package db

type SQLBuilder struct {
    buf    strings.Builder
    args   []interface{}
    where  bool
}

// 创建
func NewSQL(sql string, args ...interface{}) *SQLBuilder

// 条件追加 (对应 #where, #and)
func (b *SQLBuilder) Where(condition string, args ...interface{}) *SQLBuilder
func (b *SQLBuilder) And(condition string, args ...interface{}) *SQLBuilder
func (b *SQLBuilder) AndIf(condition string, arg interface{}, check bool) *SQLBuilder

// 排序 (对应 #orderBy)
func (b *SQLBuilder) OrderBy(order string) *SQLBuilder

// 构建
func (b *SQLBuilder) Build() (string, []interface{})
```

**使用示例：**

```go
// Java: Db.sql("select * from user #where() #and(age > ?)", 18).find()
// Go:
rows, err := db.SQL("select * from user").Where("age > ?", 18).Find()

// Java: Db.sql("select * from user #where() #and(name like ?)", "%james%").paginate(1, 10)
// Go:
page, err := db.SQL("select * from user").Where("name like ?", "%james%").Paginate(1, 10)
```

---

## 8. Operator (对应 `cn.aifei.db.sql.Operator`)

**Java 版枚举值：**
```java
EQUAL("="), NOT_EQUAL("!="), GREATER(">"), GREATER_OR_EQUAL(">="),
LESS("<"), LESS_OR_EQUAL("<="),
IN("IN"), NOT_IN("NOT IN"),
BETWEEN("BETWEEN"), NOT_BETWEEN("NOT BETWEEN"),
IS_NULL("IS NULL"), IS_NOT_NULL("IS NOT NULL"),
LIKE("LIKE"), NOT_LIKE("NOT LIKE"),
CONTAINS("LIKE %...%"), NOT_CONTAINS("NOT LIKE %...%"),
STARTS_WITH("LIKE ...%"), ENDS_WITH("LIKE %...")
```

**Go 版：**

```go
package db

type Operator string

const (
    OpEqual        Operator = "="
    OpNotEqual     Operator = "!="
    OpGreater      Operator = ">"
    OpGreaterEqual Operator = ">="
    OpLess         Operator = "<"
    OpLessEqual    Operator = "<="
    OpIn           Operator = "IN"
    OpNotIn        Operator = "NOT IN"
    OpBetween      Operator = "BETWEEN"
    OpNotBetween   Operator = "NOT BETWEEN"
    OpIsNull       Operator = "IS NULL"
    OpIsNotNull    Operator = "IS NOT NULL"
    OpLike         Operator = "LIKE"
    OpNotLike      Operator = "NOT LIKE"
)

// Contains/StartsWith/EndsWith 通过辅助函数实现
func LikeContains(value string) string { return "%" + value + "%" }
func LikeStartsWith(value string) string { return value + "%" }
func LikeEndsWith(value string) string { return "%" + value }
```

---

## 9. Batch 批量操作 (对应 `cn.aifei.db.core.Batch`)

```go
package db

type BatchResult struct {
    RowsAffected int64
    Error        error
}

type Batch struct {
    config *Config
}

func (b *Batch) Insert(rows []*Row) (*BatchResult, error)
func (b *Batch) InsertWithTable(table string, rows []*Row) (*BatchResult, error)
func (b *Batch) Update(rows []*Row) (*BatchResult, error)
func (b *Batch) UpdateWithTable(table string, rows []*Row) (*BatchResult, error)
func (b *Batch) Execute(sql string, argsList [][]interface{}) (*BatchResult, error)
func (b *Batch) ExecuteSQLs(sqls []string) (*BatchResult, error)
```

---

## 10. Transaction 事务 (对应 `cn.aifei.db.transaction.Transaction`)

**Java 版：**
```java
<R> R transaction(Atom<R> atom)
// Atom<R> 是函数式接口: R execute(Transaction tx) throws Throwable
// tx.rollback() 可手动回滚
```

**Go 版：**

```go
package db

func Transaction(fn func() error) error
func TransactionWithID(configID string, fn func() error) error

// 在事务中使用 Dao
func (d *Dao) Transaction(fn func(d *Dao) error) error
```

---

## 11. Config 数据库配置 (对应 `cn.aifei.db.core.DbConfig`)

```go
package db

type Config struct {
    ID         string
    DriverName string
    DSN        string
    Dialect    Dialect
    MaxOpen    int
    MaxIdle    int
    MaxLife    time.Duration
    Printer    func(sql string, args ...interface{})
    pool       *sql.DB
}

type ConfigOption func(*Config)

func WithDialect(d Dialect) ConfigOption
func WithMaxOpen(n int) ConfigOption
func WithMaxIdle(n int) ConfigOption
func WithMaxLife(d time.Duration) ConfigOption
func WithPrinter(fn func(string, ...interface{})) ConfigOption

func (c *Config) CreateDao() *Dao
func (c *Config) Pool() *sql.DB
```

---

## 12. TypeConverter (对应 `cn.aifei.db.core.TypeConverter`)

```go
package db

func ToInt(v interface{}) int
func ToInt64(v interface{}) int64
func ToFloat64(v interface{}) float64
func ToBool(v interface{}) bool
func ToString(v interface{}) string
func ToTime(v interface{}) time.Time
```

---

## 13. 使用示例对比

### Java 版

```java
// 插入
Row row = Row.of("user").set("name", "james").set("age", 18);
Db.insert(row);

// 查询
Page<Row> page = Db.sql("select * from user where age > #para(0)", 18)
    .paginate(1, 10);

// 更新
Db.update(Row.of("user").id(123).set("name", "james zhan"));

// 删除
Db.deleteById("user", 123);

// 事务
Db.transaction(tx -> {
    Db.sql("update account set balance = balance - ? where id = ?", 100, 1).update();
    Db.sql("update account set balance = balance + ? where id = ?", 100, 2).update();
    return Out.ok();
});
```

### Go 版

```go
// 插入
row := db.NewRow("user").Set("name", "james").Set("age", 18)
db.Insert(row)

// 查询
page, err := db.SQL("select * from user where age > ?", 18).Paginate(1, 10)

// 更新
db.Update(db.NewRow("user").ID(123).Set("name", "james zhan"))

// 删除
db.DeleteByID("user", 123)

// 事务
err := db.Transaction(func() error {
    db.SQL("update account set balance = balance - ? where id = ?", 100, 1).Update()
    db.SQL("update account set balance = balance + ? where id = ?", 100, 2).Update()
    return nil
})
```
