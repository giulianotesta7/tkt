package domain

import "time"

// Department is the top-level organizational grouping in the ticket catalog.
type Department struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// Area is a department-owned grouping. The catalog intentionally has exactly
// three levels; areas cannot contain areas or departments.
type Area struct {
	ID           int64
	DepartmentID int64
	Name         string
	Description  string
	CreatedAt    time.Time
}

// CatalogCategory is a category with its resolved hierarchy context.
type CatalogCategory struct {
	Category
	AreaName       string
	DepartmentID   int64
	DepartmentName string
}

// CatalogDepartment is a department and its category count.
type CatalogDepartment struct {
	Department
	CategoryCount int
}

// CatalogArea is an area and its category count.
type CatalogArea struct {
	Area
	CategoryCount int
}
