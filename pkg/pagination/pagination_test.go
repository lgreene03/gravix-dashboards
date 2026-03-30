package pagination

import (
	"net/http/httptest"
	"testing"
)

func TestFromRequest_Defaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/items", nil)
	p := FromRequest(r)
	if p.Page != 1 {
		t.Errorf("page = %d, want 1", p.Page)
	}
	if p.Limit != DefaultLimit {
		t.Errorf("limit = %d, want %d", p.Limit, DefaultLimit)
	}
}

func TestFromRequest_Custom(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/items?page=3&limit=50", nil)
	p := FromRequest(r)
	if p.Page != 3 {
		t.Errorf("page = %d, want 3", p.Page)
	}
	if p.Limit != 50 {
		t.Errorf("limit = %d, want 50", p.Limit)
	}
}

func TestFromRequest_MaxLimit(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/items?limit=500", nil)
	p := FromRequest(r)
	if p.Limit != MaxLimit {
		t.Errorf("limit = %d, want %d", p.Limit, MaxLimit)
	}
}

func TestFromRequest_InvalidValues(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/items?page=-1&limit=abc", nil)
	p := FromRequest(r)
	if p.Page != 1 {
		t.Errorf("page = %d, want 1", p.Page)
	}
	if p.Limit != DefaultLimit {
		t.Errorf("limit = %d, want %d", p.Limit, DefaultLimit)
	}
}

func TestOffset(t *testing.T) {
	tests := []struct {
		page, limit, want int
	}{
		{1, 20, 0},
		{2, 20, 20},
		{3, 50, 100},
	}
	for _, tt := range tests {
		p := Params{Page: tt.page, Limit: tt.limit}
		if got := p.Offset(); got != tt.want {
			t.Errorf("Offset(page=%d, limit=%d) = %d, want %d", tt.page, tt.limit, got, tt.want)
		}
	}
}

func TestNewResponse(t *testing.T) {
	p := Params{Page: 2, Limit: 10}
	r := NewResponse(p, 25)
	if r.TotalPages != 3 {
		t.Errorf("total_pages = %d, want 3", r.TotalPages)
	}
	if r.Total != 25 {
		t.Errorf("total = %d, want 25", r.Total)
	}
}

func TestNewResponse_ExactDivision(t *testing.T) {
	r := NewResponse(Params{Page: 1, Limit: 10}, 20)
	if r.TotalPages != 2 {
		t.Errorf("total_pages = %d, want 2", r.TotalPages)
	}
}

func TestNewResponse_Zero(t *testing.T) {
	r := NewResponse(Params{Page: 1, Limit: 10}, 0)
	if r.TotalPages != 0 {
		t.Errorf("total_pages = %d, want 0", r.TotalPages)
	}
}
