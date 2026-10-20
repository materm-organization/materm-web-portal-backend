package middleware

// APIResponse is a generic wrapper for external API responses with { status, data } shape.
type APIResponse[T any] struct {
	Status bool `json:"status"`
	Data   T    `json:"data"`
}

type PageQuery struct {
	Page       int         `json:"page,omitempty" query:"page" default:"1"`
	Limit      int         `json:"limit,omitempty" query:"limit" default:"10"`
	TotalPages int         `json:"totalPages"`
	TotalRows  int64       `json:"totalRows"`
	Search     string      `json:"search,omitempty" query:"search"`
	Order      string      `json:"order,omitempty" query:"order" default:"DESC"`
	SortBy     string      `json:"sort_by,omitempty" query:"sort_by" default:"id"`
	Mode       string      `json:"mode,omitempty" query:"mode" default:"normal"`
	StartDate  string      `json:"start_date,omitempty" query:"start_date"`
	EndDate    string      `json:"end_date,omitempty" query:"end_date"`
	Type       string      `json:"type,omitempty" query:"type"`
	Rows       interface{} `json:"rows"`
}

// ResolveOrderDir returns "ASC" or "DESC", defaulting to "DESC".
func (q PageQuery) ResolveOrderDir() string {
	if q.Order == "ASC" || q.Order == "asc" {
		return "ASC"
	}
	return "DESC"
}

// ResolveSortCol returns q.SortBy if it exists in allowed, otherwise defaultCol.
func (q PageQuery) ResolveSortCol(allowed map[string]bool, defaultCol string) string {
	if q.SortBy != "" && allowed[q.SortBy] {
		return q.SortBy
	}
	return defaultCol
}
