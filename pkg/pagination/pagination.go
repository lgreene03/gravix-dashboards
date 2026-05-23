// Package pagination provides server-side pagination helpers.
package pagination

import (
	"net/http"
	"strconv"
)

const (
	// DefaultLimit is the default number of items per page.
	DefaultLimit = 20
	// MaxLimit is the maximum allowed items per page.
	MaxLimit = 100
)

// Params represents pagination parameters.
type Params struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
}

// Offset returns the SQL offset for the current page.
func (p Params) Offset() int {
	return (p.Page - 1) * p.Limit
}

// Response is the standard pagination metadata in API responses.
type Response struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// NewResponse creates a pagination response from params and total count.
func NewResponse(p Params, total int) Response {
	totalPages := 0
	if p.Limit > 0 {
		totalPages = total / p.Limit
		if total%p.Limit > 0 {
			totalPages++
		}
	}
	return Response{
		Page:       p.Page,
		Limit:      p.Limit,
		Total:      total,
		TotalPages: totalPages,
	}
}

// FromRequest extracts pagination params from HTTP query parameters.
func FromRequest(r *http.Request) Params {
	page := 1
	limit := DefaultLimit

	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}

	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	if limit > MaxLimit {
		limit = MaxLimit
	}

	return Params{Page: page, Limit: limit}
}
