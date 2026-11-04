package discovery

import (
	"context"
	"testing"

	"github.com/keel-foundation/keel-core/internal/store"
)

func TestLexicalRerankerPrefersTitleAndTags(t *testing.T) {
	videos := []store.Video{
		{Title: "Unrelated essay", Description: "A general essay.", Category: "Studio"},
		{Title: "The audit ledger explained", Description: "How moderation records work.", Category: "Moderation", Tags: []string{"audit", "ledger"}},
	}
	scores, err := LexicalReranker{}.Rank(context.Background(), "audit ledger", videos)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if len(scores) != 2 || scores[1] <= scores[0] {
		t.Fatalf("expected relevant audit video to rank higher: %v", scores)
	}
}
