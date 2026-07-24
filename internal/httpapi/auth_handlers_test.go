package httpapi

import (
	"testing"
	"time"
)

func TestIsAtLeastAge(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		dateOfBirth time.Time
		expected    bool
	}{
		{
			name:        "birthday today",
			dateOfBirth: time.Date(2008, time.July, 24, 0, 0, 0, 0, time.UTC),
			expected:    true,
		},
		{
			name:        "birthday tomorrow",
			dateOfBirth: time.Date(2008, time.July, 25, 0, 0, 0, 0, time.UTC),
			expected:    false,
		},
		{
			name:        "older",
			dateOfBirth: time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC),
			expected:    true,
		},
		{
			name:        "future date",
			dateOfBirth: now.AddDate(1, 0, 0),
			expected:    false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := isAtLeastAge(test.dateOfBirth, now, 18); actual != test.expected {
				t.Fatalf("got %v, want %v", actual, test.expected)
			}
		})
	}
}
