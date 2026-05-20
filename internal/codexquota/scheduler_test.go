package codexquota

import (
	"testing"
	"time"
)

func TestNextRunAfterUsesUTC8Schedule(t *testing.T) {
	loc := UTC8()
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before morning",
			now:  time.Date(2026, 5, 20, 6, 39, 0, 0, loc),
			want: time.Date(2026, 5, 20, 6, 40, 0, 0, loc),
		},
		{
			name: "after morning",
			now:  time.Date(2026, 5, 20, 6, 40, 0, 0, loc),
			want: time.Date(2026, 5, 20, 11, 45, 0, 0, loc),
		},
		{
			name: "after last",
			now:  time.Date(2026, 5, 20, 17, 1, 0, 0, loc),
			want: time.Date(2026, 5, 21, 6, 40, 0, 0, loc),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextRunAfter(tt.now, loc, DefaultTimes)
			if !got.Equal(tt.want) {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
