package inventory

import (
	"context"
	"database/sql"
	"time"
)

// WithPGAdvisoryLeadership runs fn only while this process holds a session advisory lock.
// lockKey should be a stable number (e.g., hash of a string).
func WithPGAdvisoryLeadership(ctx context.Context, db *sql.DB, lockKey int64, fn func(context.Context)) {
	for {
		if ctx.Err() != nil {
			return
		}

		conn, err := db.Conn(ctx)
		if err != nil {
			return
		}

		var got bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&got); err != nil {
			_ = conn.Close()
			return
		}
		if !got {
			// Not leader; back off a bit
			_ = conn.Close()
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		// We are leader for the duration of this connection
		fn(ctx)

		// Release by closing the session connection
		_ = conn.Close()
		return
	}
}
