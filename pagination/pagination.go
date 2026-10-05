package pagination

import "math"

type Pagination struct {
	NumPages           int
	HasPrev, HasNext   bool
	PrevPage, NextPage int
	ItemsPerPage       int
	CurrentPage        int
	NumItems           int
	Start, End         int
}

func Calculate(currentPage, itemsPerPage, numItems int) (p Pagination) {
	p = Pagination{}

	if itemsPerPage < 1 {
		itemsPerPage = 1
	}
	if numItems < 0 {
		numItems = 0
	}

	// calc number of pages
	p.NumPages = int(math.Ceil(float64(numItems) / float64(itemsPerPage)))

	if currentPage > p.NumPages {
		currentPage = p.NumPages
	}
	if currentPage < 1 {
		currentPage = 1
	}

	p.CurrentPage = currentPage
	p.ItemsPerPage = itemsPerPage
	p.NumItems = numItems

	// calc start + end
	p.Start = (currentPage - 1) * p.ItemsPerPage

	if p.Start > p.NumItems {
		p.Start = p.NumItems
	}

	p.End = p.Start + p.ItemsPerPage
	if p.End > p.NumItems {
		p.End = p.NumItems
	}

	// HasPrev, HasNext?
	p.HasPrev = p.CurrentPage > 1
	p.HasNext = p.CurrentPage < p.NumPages

	// calculate prev + next pages
	if p.HasPrev {
		p.PrevPage = p.CurrentPage - 1
	}
	if p.HasNext {
		p.NextPage = p.CurrentPage + 1
	}

	return
}
