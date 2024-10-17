package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostGreSQLProvider_Query(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		expectedError bool
		setupFunc     func(mock sqlmock.Sqlmock)
	}{
		{
			name:          "valid query",
			query:         "SELECT 1",
			expectedError: false,
			setupFunc: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
			},
		},
		{
			name:          "sql injection attempt",
			query:         "SELECT * FROM users WHERE username = 'admin' --' AND password = 'password'",
			expectedError: true,
			setupFunc: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT \\* FROM users WHERE username = 'admin' --' AND password = 'password'").WillReturnError(fmt.Errorf("query not allowed"))
			},
		},
		{
			name:          "query with special characters",
			query:         "SELECT * FROM users WHERE username = 'admin'; DROP TABLE users;",
			expectedError: true,
			setupFunc: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT \\* FROM users WHERE username = 'admin'; DROP TABLE users;").WillReturnError(fmt.Errorf("query not allowed"))
			},
		},
		{
			name:          "query with valid parameters",
			query:         "SELECT * FROM queries WHERE statusCode = 200",
			expectedError: false,
			setupFunc: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT \\* FROM queries WHERE statusCode = 200").WillReturnRows(sqlmock.NewRows([]string{"ts", "queryParam", "statusCode"}).AddRow(time.Now(), "query1", 200))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			provider := &PostGreSQLProvider{db: db}
			tt.setupFunc(mock)

			result, err := provider.Query(ctx, tt.query)
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}
