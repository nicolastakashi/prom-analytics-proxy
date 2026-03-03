package otlp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRedisTTLSeconds(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want int64
	}{
		{name: "negative_defaults_to_one_hour", ttl: -1 * time.Second, want: 3600},
		{name: "zero_defaults_to_one_hour", ttl: 0, want: 3600},
		{name: "one_nanosecond_rounds_up_to_one_second", ttl: 1 * time.Nanosecond, want: 1},
		{name: "sub_second_rounds_up_to_one_second", ttl: 500 * time.Millisecond, want: 1},
		{name: "exact_one_second", ttl: 1 * time.Second, want: 1},
		{name: "one_and_half_seconds_rounds_up", ttl: 1500 * time.Millisecond, want: 2},
		{name: "exact_two_seconds", ttl: 2 * time.Second, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, redisTTLSeconds(tt.ttl))
		})
	}
}
