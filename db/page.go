package db

// Page represents a paginated result set.
type Page struct {
	PageNum    int    `json:"pageNum"`
	PageSize   int    `json:"pageSize"`
	TotalRows  int64  `json:"totalRows"`
	TotalPages int    `json:"totalPages"`
	Rows       []*Row `json:"rows"`
}

// NewPage creates a new Page.
func NewPage(pageNum, pageSize int, totalRows int64, rows []*Row) *Page {
	totalPages := int(totalRows) / pageSize
	if int(totalRows)%pageSize != 0 {
		totalPages++
	}
	return &Page{
		PageNum:    pageNum,
		PageSize:   pageSize,
		TotalRows:  totalRows,
		TotalPages: totalPages,
		Rows:       rows,
	}
}

// IsFirstPage returns true if this is the first page.
func (p *Page) IsFirstPage() bool { return p.PageNum == 1 }

// IsLastPage returns true if this is the last page.
func (p *Page) IsLastPage() bool { return p.PageNum >= p.TotalPages }

// HasPreviousPage returns true if there is a previous page.
func (p *Page) HasPreviousPage() bool { return p.PageNum > 1 }

// HasNextPage returns true if there is a next page.
func (p *Page) HasNextPage() bool { return p.PageNum < p.TotalPages }

// PageAs is the typed paginated result set returned by Dao.PaginateAs: P is
// the model type carried in Rows (instantiate with the model pointer —
// PaginateAs[User] yields *PageAs[*User]). Field tags mirror Page so the
// JSON envelope stays identical between typed and untyped pagination.
type PageAs[P any] struct {
	PageNum    int   `json:"pageNum"`
	PageSize   int   `json:"pageSize"`
	TotalRows  int64 `json:"totalRows"`
	TotalPages int   `json:"totalPages"`
	Rows       []P   `json:"rows"`
}

// IsFirstPage returns true if this is the first page.
func (p *PageAs[P]) IsFirstPage() bool { return p.PageNum == 1 }

// IsLastPage returns true if this is the last page.
func (p *PageAs[P]) IsLastPage() bool { return p.PageNum >= p.TotalPages }

// HasPreviousPage returns true if there is a previous page.
func (p *PageAs[P]) HasPreviousPage() bool { return p.PageNum > 1 }

// HasNextPage returns true if there is a next page.
func (p *PageAs[P]) HasNextPage() bool { return p.PageNum < p.TotalPages }
