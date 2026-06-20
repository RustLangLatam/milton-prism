package impl

import (
	"math"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
)

// NewPagination creates a new Pagination object with calculated page information
// Parameters:
//   - pageNumber: The current page number (1-based)
//   - pageSize: Number of items per page
//   - totalCount: Total number of items across all pages
//
// Returns:
//
//	A pointer to a Pagination object containing:
//	- CurrentPage: The requested page number
//	- PageSize: Number of items per page
//	- TotalSize: Total number of items
//	- TotalPages: Total number of pages based on total count and page size
//
// Note: If total pages calculation results in 0, it's set to 1 to ensure valid pagination
func NewPagination(pageNumber, pageSize uint32, totalCount uint64) *paginationv1.Pagination {
	// Calculate total number of pages by dividing total count by page size
	// and rounding up to ensure partial pages are counted
	totalPages := uint64(math.Ceil(float64(totalCount) / float64(pageSize)))

	// Ensure at least one page exists even if there are no items
	if totalPages == 0 {
		totalPages = 1
	}

	return &paginationv1.Pagination{
		CurrentPage: pageNumber, // Current page number
		PageSize:    pageSize,   // Number of items per page
		TotalSize:   totalCount, // Total number of items
		TotalPages:  totalPages, // Total number of pages
	}
}
