package worker

import (
	"testing"

	"github.com/arrorLabArts/heatcheck/internal/aiprovider"
)

func TestUrgentModerationCategories(t *testing.T) {
	categories := urgentModerationCategories(aiprovider.Moderation{
		Flagged: true,
		Categories: map[string]bool{
			"sexual/minors": true,
			"hate":          true,
		},
	})
	if len(categories) != 1 || categories[0] != "sexual/minors" {
		t.Fatalf("categories = %#v, want sexual/minors", categories)
	}
	if categories := urgentModerationCategories(aiprovider.Moderation{
		Flagged: false,
		Categories: map[string]bool{
			"sexual/minors": true,
		},
	}); len(categories) != 0 {
		t.Fatalf("unflagged categories = %#v, want none", categories)
	}
}
