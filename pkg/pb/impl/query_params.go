package impl

import (
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// PageQueryParamsDefault returns a default PageQueryParams configuration with safe values.
// Default values:
//   - Order: desc (newest first)
//   - PageNumber: 1 (first page)
//   - PageSize: 10 (items per page)
//   - Sort: "create_time" (sorts by creation timestamp)
//
// These defaults follow common API pagination best practices and provide
// a reasonable starting point for most list operations.
func PageQueryParamsDefault() *queryparamsv1.PageQueryParams {
	return &queryparamsv1.PageQueryParams{
		Order:      queryparamsv1.PageQueryParams_ORDER_DESC_UNSPECIFIED,
		PageNumber: 1,
		PageSize:   10,
		SortBy:     "create_time",
	}
}

// NormalizePaginationParams adjusts pagination parameters to fall within acceptable ranges.
// It ensures safe defaults and prevents API abuse by:
//   - Setting page number to 1 if less than 1
//   - Clamping page size between 10 and 100 (inclusive)
//   - Defaulting to "create_time" sort if empty
//   - Ensuring order is either asc or desc
//
// This is a normalization function rather than a validation function,
// as it silently corrects values rather than returning errors.
func NormalizePaginationParams(params *queryparamsv1.PageQueryParams) {
	if params == nil {
		return
	}

	// Normalize page number
	if params.PageNumber < 1 {
		params.PageNumber = 1
	}

	// Normalize page size
	switch {
	case params.PageSize < 10:
		params.PageSize = 10
	case params.PageSize > 100:
		params.PageSize = 100
	}

	// Normalize sort field
	if params.SortBy == "" {
		params.SortBy = "create_time"
	}

	// Normalize order direction
	if params.Order != queryparamsv1.PageQueryParams_ORDER_ASC &&
		params.Order != queryparamsv1.PageQueryParams_ORDER_DESC_UNSPECIFIED {
		params.Order = queryparamsv1.PageQueryParams_ORDER_ASC
	}
}
