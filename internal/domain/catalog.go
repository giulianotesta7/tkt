package domain

import "time"

// Department is the top-level organizational grouping in the ticket catalog.
type Department struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// CatalogCategory is a category with its resolved hierarchy context.
type CatalogCategory struct {
	Category
	DeskName       string
	DepartmentID   int64
	DepartmentName string
}

// CatalogDepartment is a department and its child/category counts.
type CatalogDepartment struct {
	Department
	DeskCount     int
	CategoryCount int
}

// CatalogDesk is a Desk with its category count and resolved Department name.
type CatalogDesk struct {
	Desk
	DepartmentID   int64
	CategoryCount  int
	DepartmentName string
}
