package pagination

// LoadMoreQuery is the request + response envelope for keyset ("load more")
// pagination — the cursor-based counterpart to ListQuery (reserved for public feeds). The client sends
// limit + cursor; the handler fills HasMore, NextCursor, and Rows.
//
// Flow:
//   - First page: client omits cursor (Cursor == 0).
//   - Fetch limit+1 rows; HasMore = len > limit; trim to limit.
//   - NextCursor = id of the last returned row (0 when there are no more pages).
//   - Next page: client sends cursor = NextCursor.
type LoadMoreQuery struct {
	Limit      int         `json:"limit,omitempty" query:"limit" default:"10"`
	Cursor     uint        `json:"-" query:"cursor"`
	HasMore    bool        `json:"has_more"`
	NextCursor uint        `json:"next_cursor"`
	Rows       interface{} `json:"rows"`
}

// LimitOrDefault returns the limit clamped to (0, max]; falls back to def when out of range.
func (q LoadMoreQuery) LimitOrDefault(def, max int) int {
	if q.Limit <= 0 || q.Limit > max {
		return def
	}
	return q.Limit
}

// SetResult fills the response fields. nextCursor is ignored (forced to 0) when
// hasMore is false, so a finished list always reports no next cursor.
func (q *LoadMoreQuery) SetResult(rows interface{}, hasMore bool, nextCursor uint) {
	q.Rows = rows
	q.HasMore = hasMore
	if hasMore {
		q.NextCursor = nextCursor
	} else {
		q.NextCursor = 0
	}
}
