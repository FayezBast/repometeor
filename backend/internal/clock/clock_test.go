package clock

import (
	"testing"
	"time"
)

func TestRealClockReturnsUTC(t *testing.T) {
	got := New().Now()
	if got.Location() != time.UTC {
		t.Fatalf("Real.Now() location = %v, want UTC", got.Location())
	}
}

func TestFakeAdvanceAndSet(t *testing.T) {
	start := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	f := NewFake(start)

	if !f.Now().Equal(start) {
		t.Fatalf("Now() = %v, want %v", f.Now(), start)
	}

	f.Advance(90 * time.Minute)
	want := start.Add(90 * time.Minute)
	if !f.Now().Equal(want) {
		t.Fatalf("after Advance, Now() = %v, want %v", f.Now(), want)
	}

	f.Set(start)
	if !f.Now().Equal(start) {
		t.Fatalf("after Set, Now() = %v, want %v", f.Now(), start)
	}
}

func TestFakeNormalisesToUTC(t *testing.T) {
	zone := time.FixedZone("UTC+5", 5*60*60)
	local := time.Date(2026, 8, 1, 15, 0, 0, 0, zone)
	f := NewFake(local)

	if f.Now().Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", f.Now().Location())
	}
	if h := f.Now().Hour(); h != 10 {
		t.Fatalf("hour = %d, want 10", h)
	}
}

func TestBucket(t *testing.T) {
	tests := []struct {
		name     string
		in       time.Time
		interval time.Duration
		want     time.Time
	}{
		{
			name:     "hourly truncates minutes and seconds",
			in:       time.Date(2026, 8, 1, 10, 47, 33, 123, time.UTC),
			interval: time.Hour,
			want:     time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		},
		{
			name:     "already aligned is unchanged",
			in:       time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
			interval: time.Hour,
			want:     time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		},
		{
			name:     "fifteen minute buckets",
			in:       time.Date(2026, 8, 1, 10, 47, 0, 0, time.UTC),
			interval: 15 * time.Minute,
			want:     time.Date(2026, 8, 1, 10, 45, 0, 0, time.UTC),
		},
		{
			name:     "non-positive interval falls back to one hour",
			in:       time.Date(2026, 8, 1, 10, 47, 0, 0, time.UTC),
			interval: 0,
			want:     time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Bucket(tc.in, tc.interval); !got.Equal(tc.want) {
				t.Fatalf("Bucket() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBucketConvertsToUTCBeforeTruncating(t *testing.T) {
	// 10:30 in UTC+5 is 05:30 UTC, which buckets to 05:00 UTC.
	zone := time.FixedZone("UTC+5", 5*60*60)
	in := time.Date(2026, 8, 1, 10, 30, 0, 0, zone)

	got := Bucket(in, time.Hour)
	want := time.Date(2026, 8, 1, 5, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Bucket() = %v, want %v", got, want)
	}
}
