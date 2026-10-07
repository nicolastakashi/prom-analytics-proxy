package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidateSortField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		sortBy         string
		sortOrder      string
		validFields    map[string]bool
		defaultSort    string
		expectedSortBy string
		expectedOrder  string
	}{
		{
			name:           "valid sort field and order",
			sortBy:         "timestamp",
			sortOrder:      "desc",
			validFields:    map[string]bool{"timestamp": true, "duration": true},
			defaultSort:    "timestamp",
			expectedSortBy: "timestamp",
			expectedOrder:  "desc",
		},
		{
			name:           "empty sort by",
			sortBy:         "",
			sortOrder:      "asc",
			validFields:    map[string]bool{"timestamp": true, "duration": true},
			defaultSort:    "timestamp",
			expectedSortBy: "timestamp",
			expectedOrder:  "asc",
		},
		{
			name:           "empty sort order",
			sortBy:         "duration",
			sortOrder:      "",
			validFields:    map[string]bool{"timestamp": true, "duration": true},
			defaultSort:    "timestamp",
			expectedSortBy: "duration",
			expectedOrder:  "desc",
		},
		{
			name:           "invalid sort field",
			sortBy:         "invalid_field",
			sortOrder:      "desc",
			validFields:    map[string]bool{"timestamp": true, "duration": true},
			defaultSort:    "timestamp",
			expectedSortBy: "timestamp",
			expectedOrder:  "desc",
		},
		{
			name:           "both empty",
			sortBy:         "",
			sortOrder:      "",
			validFields:    map[string]bool{"timestamp": true, "duration": true},
			defaultSort:    "timestamp",
			expectedSortBy: "timestamp",
			expectedOrder:  "desc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortBy := tt.sortBy
			sortOrder := tt.sortOrder

			ValidateSortField(&sortBy, &sortOrder, tt.validFields, tt.defaultSort)

			assert.Equal(t, tt.expectedSortBy, sortBy)
			assert.Equal(t, tt.expectedOrder, sortOrder)
		})
	}
}

func TestValidatePagination(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		page            int
		pageSize        int
		defaultPageSize int
		expectedPage    int
		expectedSize    int
	}{
		{
			name:            "valid pagination",
			page:            2,
			pageSize:        20,
			defaultPageSize: 10,
			expectedPage:    2,
			expectedSize:    20,
		},
		{
			name:            "zero page",
			page:            0,
			pageSize:        20,
			defaultPageSize: 10,
			expectedPage:    1,
			expectedSize:    20,
		},
		{
			name:            "negative page",
			page:            -1,
			pageSize:        20,
			defaultPageSize: 10,
			expectedPage:    1,
			expectedSize:    20,
		},
		{
			name:            "zero page size",
			page:            1,
			pageSize:        0,
			defaultPageSize: 10,
			expectedPage:    1,
			expectedSize:    10,
		},
		{
			name:            "negative page size",
			page:            1,
			pageSize:        -5,
			defaultPageSize: 10,
			expectedPage:    1,
			expectedSize:    10,
		},
		{
			name:            "both invalid",
			page:            0,
			pageSize:        0,
			defaultPageSize: 10,
			expectedPage:    1,
			expectedSize:    10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := tt.page
			pageSize := tt.pageSize

			ValidatePagination(&page, &pageSize, tt.defaultPageSize)

			assert.Equal(t, tt.expectedPage, page)
			assert.Equal(t, tt.expectedSize, pageSize)
		})
	}
}

func TestCalculateTotalPages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		totalCount int
		pageSize   int
		expected   int
	}{
		{
			name:       "exact division",
			totalCount: 100,
			pageSize:   10,
			expected:   10,
		},
		{
			name:       "remainder",
			totalCount: 105,
			pageSize:   10,
			expected:   11,
		},
		{
			name:       "single page",
			totalCount: 5,
			pageSize:   10,
			expected:   1,
		},
		{
			name:       "empty result",
			totalCount: 0,
			pageSize:   10,
			expected:   0,
		},
		{
			name:       "page size larger than total",
			totalCount: 5,
			pageSize:   20,
			expected:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateTotalPages(tt.totalCount, tt.pageSize)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTimeRange_Format(t *testing.T) {
	t.Parallel()
	tr := TimeRange{
		From: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		To:   time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC),
	}

	fromStr, toStr := tr.Format(time.RFC3339)

	expectedFrom := "2023-01-01T12:00:00Z"
	expectedTo := "2023-01-15T12:00:00Z"

	assert.Equal(t, expectedFrom, fromStr)
	assert.Equal(t, expectedTo, toStr)
}

func TestTimeRange_Previous(t *testing.T) {
	t.Parallel()
	tr := TimeRange{
		From: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		To:   time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC),
	}

	previous := tr.Previous()

	expectedFrom := time.Date(2022, 12, 18, 12, 0, 0, 0, time.UTC)
	expectedTo := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, expectedFrom, previous.From)
	assert.Equal(t, expectedTo, previous.To)
}

func TestGetDbProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		provider    DatabaseProvider
		expectError bool
	}{
		{
			name:        "postgresql provider",
			provider:    PostGreSQL,
			expectError: true, // Will fail due to missing database connection
		},
		{
			name:        "sqlite provider",
			provider:    SQLite,
			expectError: false, // Can succeed by creating a database file
		},
		{
			name:        "invalid provider",
			provider:    "invalid",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := GetDbProvider(context.Background(), tt.provider)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, provider)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, provider)
			}
		})
	}
}
