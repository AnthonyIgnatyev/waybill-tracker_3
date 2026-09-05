package handlers

import (
	"net/http"
	"net/url"
	"strconv"
)

// PaginationView — данные для отображения пагинации в шаблоне.
type PaginationView struct {
	Page       int
	PageSize   int
	TotalItems int
	TotalPages int

	HasPrev bool
	HasNext bool

	PrevURL string
	NextURL string

	From int
	To   int
}

// pageURL формирует URL для нужной страницы, сохраняя текущие query-параметры.
// Для первой страницы параметр page убирается.
func pageURL(u *url.URL, page int) string {
	q := u.Query()

	if page <= 1 {
		q.Del("page")
	} else {
		q.Set("page", strconv.Itoa(page))
	}

	encoded := q.Encode()
	if encoded == "" {
		return u.Path
	}

	return u.Path + "?" + encoded
}

// buildPagination считает страницу, общее количество страниц и ссылки назад/вперёд.
func buildPagination(r *http.Request, totalItems, pageSize int) PaginationView {
	if pageSize <= 0 {
		pageSize = 50
	}

	if totalItems < 0 {
		totalItems = 0
	}

	totalPages := (totalItems + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}

	page := parseOptInt(r.FormValue("page"), 1)
	if page < 1 {
		page = 1
	}

	if page > totalPages {
		page = totalPages
	}

	from := 0
	to := 0

	if totalItems > 0 {
		from = (page-1)*pageSize + 1
		to = page * pageSize

		if to > totalItems {
			to = totalItems
		}
	}

	p := PaginationView{
		Page:       page,
		PageSize:   pageSize,
		TotalItems: totalItems,
		TotalPages: totalPages,
		From:       from,
		To:         to,
	}

	if page > 1 {
		p.HasPrev = true
		p.PrevURL = pageURL(r.URL, page-1)
	}

	if page < totalPages {
		p.HasNext = true
		p.NextURL = pageURL(r.URL, page+1)
	}

	return p
}
