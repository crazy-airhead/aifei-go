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
