// Package sqlrepo SQL 类向量仓储后端（对标 mysql 与 mariadb 两模块）：
// mysql = JSON 存 embedding + 服务端 COSINE_SIMILARITY 存储函数（纯 SQL 余弦，
// 全表扫描——Java 原语义）；mariadb = 原生 VECTOR 列 + VEC_DISTANCE_EUCLIDEAN
// 服务端排序 + 客户端余弦重算。走 database/sql（复用 ./db 生态）。
package sqlrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
)

// SQLFlavor SQL 方言分支。
type SQLFlavor uint8

const (
	// FlavorMySQL JSON embedding + 存储函数余弦（Java MySqlRepository 同款）。
	FlavorMySQL SQLFlavor = iota
	// FlavorMariaDB 原生 VECTOR 列 + VEC_DISTANCE_EUCLIDEAN。
	FlavorMariaDB
)

// SQLEmbedder 嵌入面。
type SQLEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedDocs(ctx context.Context, docs []*rag.Document) error
	Dimensions(ctx context.Context) (int, error)
	BatchSize() int
}

// SQLRepository mysql/mariadb 共用后端。
type SQLRepository struct {
	db        *sql.DB
	embedder  SQLEmbedder
	flavor    SQLFlavor
	table     string
	metadata  []ragrepo.MetadataFieldX
}

// MetadataFieldX 元数据列（复用 ragrepo 根包的形态——SQL 侧 TEXT/NUMERIC/JSON）。
// ragrepo.MetadataField 与 es.FieldType 形态不同，此处独立定义。
type MetadataFieldX = ragrepo.MetadataFieldX

// NewMySQLRepository MySQL 后端（JSON embedding + 存储函数）。
func NewMySQLRepository(db *sql.DB, embedder SQLEmbedder, table string, metadata ...MetadataFieldX) (*SQLRepository, error) {
	r := &SQLRepository{db: db, embedder: embedder, flavor: FlavorMySQL, table: table, metadata: metadata}
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// NewMariaDBRepository MariaDB 后端（VECTOR 列）。
func NewMariaDBRepository(db *sql.DB, embedder SQLEmbedder, table string, metadata ...MetadataFieldX) (*SQLRepository, error) {
	r := &SQLRepository{db: db, embedder: embedder, flavor: FlavorMariaDB, table: table, metadata: metadata}
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// InitRepository 建表（mysql：JSON 列 + COSINE_SIMILARITY 函数；mariadb：
// VECTOR 列 + VECTOR INDEX）。
func (r *SQLRepository) InitRepository(ctx context.Context) error {
	if r.flavor == FlavorMySQL {
		if err := r.createCosineFunction(ctx); err != nil {
			return err
		}
	}
	var sb strings.Builder
	sb.WriteString("CREATE TABLE IF NOT EXISTS `" + r.table + "` (")
	sb.WriteString("`id` VARCHAR(255) PRIMARY KEY, `content` TEXT NOT NULL, ")
	if r.flavor == FlavorMySQL {
		sb.WriteString("`embedding` JSON, ")
	} else {
		dims, err := r.embedder.Dimensions(ctx)
		if err != nil {
			return err
		}
		sb.WriteString("`embedding` VECTOR(" + strconv.Itoa(dims) + ") NOT NULL, ")
	}
	sb.WriteString("`metadata` JSON, `created_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP")
	for _, f := range r.metadata {
		sb.WriteString(", `" + f.Name + "` " + sqlColumnType(f.Type))
	}
	sb.WriteString(")")
	if _, err := r.db.ExecContext(ctx, sb.String()); err != nil {
		return fmt.Errorf("ragrepo/sql: create table: %w", err)
	}
	if r.flavor == FlavorMariaDB {
		idx := fmt.Sprintf("CREATE VECTOR INDEX IF NOT EXISTS `idx_%s_embedding` ON `%s` (`embedding`) M=6 DISTANCE=euclidean",
			r.table, r.table)
		if _, err := r.db.ExecContext(ctx, idx); err != nil {
			// 索引失败非致命（老版本无 VECTOR INDEX）
			_ = idx
		}
	}
	return nil
}

// createCosineFunction 纯 SQL 余弦存储函数（Java 逐句照搬——JSON_LENGTH/
// JSON_EXTRACT 循环点积范数）。
func (r *SQLRepository) createCosineFunction(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, "DROP FUNCTION IF EXISTS COSINE_SIMILARITY"); err != nil {
		return nil // 无权限时静默（假定已存在）
	}
	fn := `CREATE FUNCTION COSINE_SIMILARITY(doc JSON, query JSON) RETURNS DOUBLE
DETERMINISTIC NO SQL
BEGIN
  DECLARE n INT DEFAULT JSON_LENGTH(doc);
  DECLARE qn INT DEFAULT JSON_LENGTH(query);
  DECLARE i INT DEFAULT 0;
  DECLARE dot DOUBLE DEFAULT 0;
  DECLARE n1 DOUBLE DEFAULT 0;
  DECLARE n2 DOUBLE DEFAULT 0;
  IF n <> qn OR n = 0 THEN RETURN 0; END IF;
  WHILE i < n DO
    SET dot = dot + JSON_EXTRACT(doc, '$[' || i || ']') * JSON_EXTRACT(query, '$[' || i || ']');
    SET n1 = n1 + JSON_EXTRACT(doc, '$[' || i || ']') * JSON_EXTRACT(doc, '$[' || i || ']');
    SET n2 = n2 + JSON_EXTRACT(query, '$[' || i || ']') * JSON_EXTRACT(query, '$[' || i || ']');
    SET i = i + 1;
  END WHILE;
  IF n1 = 0 OR n2 = 0 THEN RETURN 0; END IF;
  RETURN dot / (SQRT(n1) * SQRT(n2));
END`
	_, err := r.db.ExecContext(ctx, fn)
	if err != nil {
		return fmt.Errorf("ragrepo/sql: create cosine function: %w", err)
	}
	return nil
}

func sqlColumnType(t ragrepo.FieldTypeX) string {
	switch t {
	case ragrepo.FieldTextX:
		return "TEXT"
	case ragrepo.FieldNumericX:
		return "DECIMAL(65,30)"
	default:
		return "JSON"
	}
}

// DropRepository 删表。
func (r *SQLRepository) DropRepository(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "DROP TABLE IF EXISTS `"+r.table+"`")
	return err
}

// Search 简单检索。
func (r *SQLRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// SearchQuery 条件检索。
func (r *SQLRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/sql: query text cannot be empty")
	}
	vec, err := r.embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}

	queryVec := ragrepo.VectorLiteral(vec)
	args := []any{queryVec, queryVec, limit}
	expr := r.similarityExpr()
	if q.FilterExpr != "" {
		if node, ok := ragrepo.ParseFilterExpr(q.FilterExpr); ok {
			clause, fargs, ok := ragrepo.ToSQLWhere(node, r.fieldCol)
			if ok {
				args = append([]any{queryVec}, fargs...)
				args = append(args, queryVec, limit)
				expr = r.similarityExprWithWhere(clause)
			}
		}
	}

	rows, err := r.db.QueryContext(ctx, expr, args...)
	if err != nil {
		return nil, fmt.Errorf("ragrepo/sql: search: %w", err)
	}
	defer rows.Close()

	var docs []*rag.Document
	for rows.Next() {
		var id, content string
		var metadataJSON sql.NullString
		var embeddingRaw sql.NullString
		var similarity float64
		if err := rows.Scan(&id, &content, &metadataJSON, &embeddingRaw, &similarity); err != nil {
			return nil, err
		}
		doc := rag.NewDocument(content)
		doc.ID = id
		if metadataJSON.Valid && metadataJSON.String != "" {
			_ = json.Unmarshal([]byte(metadataJSON.String), &doc.Metadata)
		}
		// mariadb：服务端欧氏排序，客户端余弦重算（Java 同款）
		if r.flavor == FlavorMariaDB && embeddingRaw.Valid {
			if docVec := parseVectorLiteral(embeddingRaw.String); len(docVec) == len(vec) {
				similarity = cosineSimilarity(vec, docVec)
			}
		}
		doc.SetScore(similarity)
		docs = append(docs, doc)
	}
	return ragrepo.Refilter(docs, q)
}

func (r *SQLRepository) similarityExpr() string {
	if r.flavor == FlavorMySQL {
		return "SELECT `id`, `content`, `metadata`, `embedding`, COSINE_SIMILARITY(`embedding`, ?) AS similarity FROM `" +
			r.table + "` ORDER BY similarity DESC LIMIT ?"
	}
	return "SELECT `id`, `content`, `metadata`, VEC_ToText(`embedding`), VEC_DISTANCE_EUCLIDEAN(`embedding`, VEC_FromText(?)) AS d FROM `" +
		r.table + "` ORDER BY d LIMIT ?"
}

func (r *SQLRepository) similarityExprWithWhere(clause string) string {
	if r.flavor == FlavorMySQL {
		return "SELECT `id`, `content`, `metadata`, `embedding`, COSINE_SIMILARITY(`embedding`, ?) AS similarity FROM `" +
			r.table + "` WHERE " + clause + " ORDER BY similarity DESC LIMIT ?"
	}
	return "SELECT `id`, `content`, `metadata`, VEC_ToText(`embedding`), VEC_DISTANCE_EUCLIDEAN(`embedding`, VEC_FromText(?)) AS d FROM `" +
		r.table + "` WHERE " + clause + " ORDER BY d LIMIT ?"
}

func (r *SQLRepository) fieldCol(name string) (string, bool) {
	for _, f := range r.metadata {
		if f.Name == name {
			return "`" + name + "`", true
		}
	}
	return "JSON_UNQUOTE(JSON_EXTRACT(`metadata`, '$." + name + "'))", true
}

// Save 批量写入（upsert ON DUPLICATE KEY）。
func (r *SQLRepository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
	base := ragrepo.RestRepositoryBase{Embedder: sqlEmbedderAdapter{r.embedder}}
	return base.SaveBatches(ctx, docs, func(ctx context.Context, batch []*rag.Document) error {
		cols := []string{"`id`", "`content`", "`embedding`", "`metadata`"}
		for _, f := range r.metadata {
			cols = append(cols, "`"+f.Name+"`")
		}
		placeholders := make([]string, len(cols))
		updates := []string{"`content` = VALUES(`content`)", "`embedding` = VALUES(`embedding`)", "`metadata` = VALUES(`metadata`)"}
		for i := range placeholders {
			placeholders[i] = "?"
		}
		for _, f := range r.metadata {
			updates = append(updates, "`"+f.Name+"` = VALUES(`"+f.Name+"`)")
		}
		stmtStr := "INSERT INTO `" + r.table + "` (" + strings.Join(cols, ", ") + ") VALUES (" +
			strings.Join(placeholders, ", ") + ") ON DUPLICATE KEY UPDATE " + strings.Join(updates, ", ")

		stmt, err := r.db.PrepareContext(ctx, stmtStr)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, doc := range batch {
			metaJSON, _ := json.Marshal(doc.Metadata)
			embStr := ragrepo.VectorLiteral(doc.Embedding)
			var embArg any
			if r.flavor == FlavorMariaDB {
				embArg = sql.NullString{String: embStr, Valid: true}
			} else {
				embArg = embStr
			}
			args := []any{doc.ID, doc.Content, embArg, string(metaJSON)}
			for _, f := range r.metadata {
				args = append(args, metadataValueX(doc, f))
			}
			if _, err := stmt.ExecContext(ctx, args...); err != nil {
				return fmt.Errorf("ragrepo/sql: upsert %s: %w", doc.ID, err)
			}
		}
		return nil
	}, progress)
}

type sqlEmbedderAdapter struct{ SQLEmbedder }

func metadataValueX(doc *rag.Document, f MetadataFieldX) any {
	v, has := doc.Metadata[f.Name]
	if !has || v == nil {
		return nil
	}
	switch f.Type {
	case ragrepo.FieldTextX:
		return fmt.Sprint(v)
	case ragrepo.FieldNumericX:
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return n
		}
		return nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return string(b)
	}
}

// DeleteByID 删除。
func (r *SQLRepository) DeleteByID(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	_, err := r.db.ExecContext(ctx,
		"DELETE FROM `"+r.table+"` WHERE `id` IN ("+strings.Join(placeholders, ",")+")", args...)
	return err
}

// ExistsByID 存在性。
func (r *SQLRepository) ExistsByID(ctx context.Context, id string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+r.table+"` WHERE `id` = ?", id).Scan(&count)
	return count > 0, err
}

// cosineSimilarity 客户端余弦（mariadb 路径）。
func cosineSimilarity(a, b []float32) float64 {
	var dot, n1, n2 float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		n1 += float64(a[i]) * float64(a[i])
		n2 += float64(b[i]) * float64(b[i])
	}
	if n1 == 0 || n2 == 0 {
		return 0
	}
	return dot / (math.Sqrt(n1) * math.Sqrt(n2))
}

// parseVectorLiteral "[0.1,0.2]" → []float32。
func parseVectorLiteral(s string) []float32 {
	s = strings.TrimPrefix(strings.TrimSuffix(s, "]"), "[")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]float32, 0, len(parts))
	for _, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil
		}
		out = append(out, float32(f))
	}
	return out
}
