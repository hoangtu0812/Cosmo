package httpapi

import (
	"crypto/sha256"
	"sort"
	"strings"

	"cosmo/backend/internal/knowledge"
)

// fuseKnowledgeRanks is the fallback when no shared reranker/provider has been
// authorised. Each corpus contributes one ranked list; raw scores remain local
// to that list. Equal rank is resolved by provenance, never response timing or
// score magnitude. This balances candidates, but is not a semantic reranker.
func fuseKnowledgeRanks(lists [][]knowledge.Passage, limit int) []knowledge.Passage {
	type identity struct {
		kb, document, section, page string
		text                        [32]byte
	}
	seen := make(map[identity]bool)
	result := make([]knowledge.Passage, 0)
	for _, list := range lists {
		rank := 0
		for _, passage := range list {
			if strings.TrimSpace(passage.Text) == "" {
				continue
			}
			key := identity{passage.KBID, passage.DocumentID, passage.Section, passage.Page, sha256.Sum256([]byte(passage.Text))}
			if seen[key] {
				continue
			}
			seen[key] = true
			rank++
			passage.LocalRank = rank
			passage.FusionScore = 1 / float64(60+rank)
			result = append(result, passage)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.FusionScore != b.FusionScore {
			return a.FusionScore > b.FusionScore
		}
		return a.KBID < b.KBID
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}
