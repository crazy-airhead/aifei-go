// Package pgvector pgvector 向量仓储（对标 solon-ai-repo-pgvector 的
// PgVectorRepository；复用 database/sql）。表结构：id/content/embedding
// VECTOR(dims)/metadata JSONB/created_at + 元数据列（TEXT/NUMERIC/JSONB 平铺）；
// ivfflat cosine 索引；检索 1 − (embedding <=> ?::vector) 相似度 + 余弦排序。
//
// 测试面说明（05 §9.5）：SQL 骨架经导出的 Build* 函数做黄金断言（无外部
// 服务）；真连测试以 RAG_PGVECTOR_DSN 环境变量开关（FLOW_MYSQL_DSN 先例），
// 缺省跳过。
package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
)

// FieldType 元数据列类型（对标 pgvector MetadataField.FieldType 的三值面）。
type FieldType string

const (
	FieldText    FieldType = "TEXT"
	FieldNumeric FieldType = "NUMERIC"
	FieldJSON    FieldType = "JSONB"
)

// MetadataField 元数据列。
type MetadataField struct {
	Name string
	Type FieldType
}

// Repository pgvector 向量仓储。
type Repository struct {
	db            *sql.DB
	embedder      embedder
	table         string
	metadataField []MetadataField
	embeddingDim  int
}

type embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedDocs(ctx context.Context, docs []*rag.Document) error
	Dimensions(ctx context.Context) (int, error)
	BatchSize() int
}

// Option 仓储选项。
type Option func(*Repository)

// WithMetadataFields 元数据列（建表 + 过滤目标）。
func WithMetadataFields(fields ...MetadataField) Option {
	return func(r *Repository) { r.metadataField = append(r.metadataField, fields...) }
}

// NewRepository 构造并初始化（对标 Java 构造期 initRepository）。
func NewRepository(db *sql.DB, model embedder, tableName string, opts ...Option) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("ragrepo/pgvector: db is required")
	}
	if model == nil {
		return nil, fmt.Errorf("ragrepo/pgvector: embedding model is required")
	}
	r := &Repository{db: db, embedder: model, table: tableName}
	for _, o := range opts {
		o(r)
	}
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// InitRepository 扩展安装 + 建表 + ivfflat 索引。
func (r *Repository) InitRepository(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return fmt.Errorf("ragrepo/pgvector: create extension: %w", err)
	}
	var exists bool
	err := r.db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = $1)", r.table).Scan(&exists)
	if err != nil {
		return fmt.Errorf("ragrepo/pgvector: table exists check: %w", err)
	}
	if exists {
		return nil
	}
	dims, err := r.embedder.Dimensions(ctx)
	if err != nil {
		return err
	}
	r.embeddingDim = dims

	ddl := BuildCreateTableDDL(r.table, dims, r.metadataField)
	if _, err := r.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ragrepo/pgvector: create table: %w", err)
	}
	idx := fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS idx_%s_embedding ON %s USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)",
		r.table, r.table)
	if _, err := r.db.ExecContext(ctx, idx); err != nil {
		return fmt.Errorf("ragrepo/pgvector: create index: %w", err)
	}
	return nil
}

// DropRepository 注销表。
func (r *Repository) DropRepository(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+r.table)
	return err
}

// Search 简单检索。
func (r *Repository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// TransformFilter 实现 rag.FilterTransformer：enjoy 子集 → SQL WHERE（值参数
// 化——Java 内联字符串的注入面在 Go 收口）。字段解析：声明的元数据列直引，
// 其余走 metadata->>'k' 的 JSON 提取（文本比较）。
func (r *Repository) TransformFilter(expr string) (any, bool) {
	node, ok := ragrepo.ParseFilterExpr(expr)
	if !ok {
		return nil, false
	}
	fields := map[string]FieldType{}
	for _, f := range r.metadataField {
		fields[f.Name] = f.Type
	}
	clause, args, ok := ragrepo.ToSQLWhere(node, func(name string) (string, bool) {
		if t, isCol := fields[name]; isCol {
			_ = t
			return `"` + name + `"`, true
		}
		return "(metadata->>'" + name + "')", true
	})
	if !ok {
		return nil, false
	}
	return SQLFilter{Clause: clause, Args: args}, true
}

// SQLFilter 原生 SQL 过滤片段（TransformFilter 的返回形态）。
type SQLFilter struct {
	Clause string
	Args   []any
}

// SearchQuery 条件检索：embed → 余弦相似度 SELECT（可选 WHERE）→ Refilter。
func (r *Repository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/pgvector: query text cannot be empty")
	}
	vec, err := r.embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}

	queryStr := BuildSearchSQL(r.table, "", 0)
	args := []any{VectorLiteral(vec), VectorLiteral(vec), limit}
	if q.FilterExpr != "" {
		if f, ok := r.TransformFilter(q.FilterExpr); ok {
			sf := f.(SQLFilter)
			queryStr = BuildSearchSQL(r.table, sf.Clause, len(sf.Args))
			args = append([]any{VectorLiteral(vec)}, sf.Args...)
			args = append(args, VectorLiteral(vec), limit)
		}
	}

	rows, err := r.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("ragrepo/pgvector: search: %w", err)
	}
	defer rows.Close()

	var docs []*rag.Document
	for rows.Next() {
		var id, content string
		var metadataJSON sql.NullString
		var similarity float64
		if err := rows.Scan(&id, &content, &metadataJSON, &similarity); err != nil {
			return nil, err
		}
		doc := rag.NewDocument(content)
		doc.ID = id
		if metadataJSON.Valid && metadataJSON.String != "" {
			_ = json.Unmarshal([]byte(metadataJSON.String), &doc.Metadata)
		}
		doc.SetScore(similarity)
		docs = append(docs, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rag.Refilter(docs, q)
}

// ---- Storer ----

// Save 分批嵌入 + upsert（ON CONFLICT (id) DO UPDATE；进度按批回调）。
func (r *Repository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
	if len(docs) == 0 {
		if progress != nil {
			progress(0, 0)
		}
		return nil
	}
	for _, doc := range docs {
		if doc.ID == "" {
			doc.ID = newUUID()
		}
	}
	batchSize := maxInt(r.embedder.BatchSize(), 1)
	batches := (len(docs) + batchSize - 1) / batchSize
	for i := 0; i < batches; i++ {
		batch := docs[i*batchSize : minInt((i+1)*batchSize, len(docs))]
		if err := r.embedder.EmbedDocs(ctx, batch); err != nil {
			return err
		}
		stmt, err := r.db.PrepareContext(ctx, buildUpsertSQL(r.table, r.metadataField))
		if err != nil {
			return err
		}
		for _, doc := range batch {
			metaJSON, _ := json.Marshal(doc.Metadata)
			args := []any{doc.ID, doc.Content, VectorLiteral(doc.Embedding), string(metaJSON)}
			for _, f := range r.metadataField {
				args = append(args, metadataColumnValue(doc, f))
			}
			if _, err := stmt.ExecContext(ctx, args...); err != nil {
				stmt.Close()
				return fmt.Errorf("ragrepo/pgvector: upsert %s: %w", doc.ID, err)
			}
		}
		stmt.Close()
		if progress != nil {
			progress(i+1, batches)
		}
	}
	return nil
}

// DeleteByID 批量删除。
func (r *Repository) DeleteByID(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, "DELETE FROM "+r.table+" WHERE id = ANY($1::text[])", pqArray(ids))
	return err
}

// ExistsByID 存在性探测。
func (r *Repository) ExistsByID(ctx context.Context, id string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+r.table+" WHERE id = $1", id).Scan(&count)
	return count > 0, err
}

// pqArray Postgres 数组字面量（ANY($1) 的字符串形态；避免引入 pq 依赖）。
func pqArray(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = `"` + strings.ReplaceAll(id, `"`, `\"`) + `"`
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

// metadataColumnValue 元数据列取值（NUMERIC 非数字置 NULL；TEXT toString；
// JSON 序列化）。
func metadataColumnValue(doc *rag.Document, f MetadataField) any {
	v, has := doc.Metadata[f.Name]
	if !has || v == nil {
		return nil
	}
	switch f.Type {
	case FieldText:
		return fmt.Sprint(v)
	case FieldNumeric:
		if n, ok := v.(float64); ok {
			return n
		}
		if n, ok := v.(int); ok {
			return n
		}
		if n, ok := v.(int64); ok {
			return n
		}
		return nil
	case FieldJSON:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return string(b)
	}
	return nil
}

// ---- SQL 骨架导出面（测试/诊断；05 §9.5 的无服务验证缝）----

// BuildCreateTableDDL 建表语句。
func BuildCreateTableDDL(table string, dims int, fields []MetadataField) string {
	var sb strings.Builder
	sb.WriteString("CREATE TABLE ")
	sb.WriteString(table)
	sb.WriteString(" (id VARCHAR(255) PRIMARY KEY, content TEXT NOT NULL, embedding VECTOR(")
	sb.WriteString(strconv.Itoa(dims))
	sb.WriteString("), metadata JSONB, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP")
	for _, f := range fields {
		sb.WriteString(", \"")
		sb.WriteString(f.Name)
		sb.WriteString("\" ")
		sb.WriteString(string(f.Type))
	}
	sb.WriteString(")")
	return sb.String()
}

// BuildSearchSQL 检索语句（filterClause 空 = 无 WHERE，filterArgs = 过滤参数
// 个数——占位符随过滤参数偏移）。
func BuildSearchSQL(table, filterClause string, filterArgs int) string {
	q := "SELECT id, content, metadata, 1 - (embedding <=> $1::vector) as similarity FROM " + table
	orderVec, limit := 2, 3
	if filterClause != "" {
		q += " WHERE " + filterClause
		orderVec = 2 + filterArgs
		limit = 3 + filterArgs
	}
	return q + " ORDER BY embedding <=> $" + strconv.Itoa(orderVec) + "::vector LIMIT $" + strconv.Itoa(limit)
}

// BuildUpsertSQL 写入语句（columns 为元数据列名集，可空）。
func BuildUpsertSQL(table string, columns []string) string {
	cols := []string{"id", "content", "embedding", "metadata"}
	for _, f := range columns {
		cols = append(cols, `"`+f+`"`)
	}
	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = "$" + strconv.Itoa(i+1)
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(table)
	sb.WriteString(" (")
	sb.WriteString(strings.Join(cols, ", "))
	sb.WriteString(") VALUES (")
	sb.WriteString(strings.Join(placeholders, ", "))
	sb.WriteString(") ON CONFLICT (id) DO UPDATE SET content = EXCLUDED.content, embedding = EXCLUDED.embedding, metadata = EXCLUDED.metadata")
	for _, f := range columns {
		sb.WriteString(", \"" + f + "\" = EXCLUDED.\"" + f + "\"")
	}
	return sb.String()
}

// buildUpsertSQL 仓储内部的列实名版。
func buildUpsertSQL(table string, fields []MetadataField) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return BuildUpsertSQL(table, names)
}

// VectorLiteral 向量的 SQL 字面量（'[0.1,0.2]'::vector 的参数形态）。
func VectorLiteral(vec []float32) string {
	parts := make([]string, len(vec))
	for i, v := range vec {
		parts[i] = strconv.FormatFloat(float64(v), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// newUUID UUIDv4 形态。
func newUUID() string {
	return ragrepo.NewUUID()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ rag.Repository = (*Repository)(nil)
var _ rag.Storer = (*Repository)(nil)
var _ rag.Lifecycle = (*Repository)(nil)
var _ rag.FilterTransformer = (*Repository)(nil)
