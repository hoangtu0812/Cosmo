// Command retrievalreport measures what a relevance floor would cost.
//
// Vector search returns nearest neighbours, and "nearest" is never "none": a
// question the knowledge base has nothing to say about still matches whatever
// is least far away. A floor would drop those, and the only honest way to pick
// one is from this deployment's own numbers - scores are not comparable across
// embedding models, rerankers, or corpora, so a constant taken from anywhere
// else is a guess wearing a decimal point.
//
// The label is free and already collected. An answer cites the passages it
// used, so a retrieved passage whose document the answer cited was worth
// having, and one no answer ever mentioned was not. The floor to want is the
// highest one that still keeps every passage an answer relied on.
//
// Read it against real traffic: turn on KNOWLEDGE_RETRIEVAL_LOG, let people
// ask for a week - including the questions that have nothing to do with the
// documents, because those are half the measurement - then run this.
//
//	go run ./cmd/retrievalreport
//	go run ./cmd/retrievalreport -since 168h -workspace wsp_123
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type loggedPassage struct {
	DocumentID string  `json:"document_id"`
	Score      float64 `json:"score"`
}

// observation is one retrieved passage, and whether the answer it fed went on
// to cite the document it came from.
type observation struct {
	Query      string
	Score      float64
	WasCited   bool
	MessageID  string
	DocumentID string
}

func main() {
	since := flag.Duration("since", 30*24*time.Hour, "how far back to read")
	workspace := flag.String("workspace", "", "limit to one workspace id")
	steps := flag.Int("steps", 20, "how many candidate floors to try")
	flag.Parse()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fail("DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		fail("connect: %v", err)
	}
	defer pool.Close()

	observations, queries, unlinked, err := read(ctx, pool, *since, *workspace)
	if err != nil {
		fail("read: %v", err)
	}
	if len(observations) == 0 {
		fmt.Println("No retrievals with a linked answer in this window.")
		fmt.Println("Set KNOWLEDGE_RETRIEVAL_LOG=true, ask some questions, and run this again.")
		if unlinked > 0 {
			fmt.Printf("\n%d retrievals were logged before answers were linked to them; those cannot be scored.\n", unlinked)
		}
		return
	}
	report(observations, queries, unlinked, *steps)
}

// read joins each logged retrieval to the answer it fed. A retrieval whose
// answer is gone - the conversation was deleted, or the turn failed - is
// counted and dropped: it has scores but no verdict, and a verdict is the
// entire point.
func read(ctx context.Context, pool *pgxpool.Pool, since time.Duration, workspace string) ([]observation, int, int, error) {
	rows, err := pool.Query(ctx, `
		SELECT l.query, l.passages, l.message_id, m.citations
		FROM knowledge_retrieval_log l
		LEFT JOIN messages m ON m.id = l.message_id
		WHERE l.created_at > NOW() - $1::interval
		  AND ($2 = '' OR l.workspace_id = $2)
		ORDER BY l.created_at`, fmt.Sprintf("%d seconds", int(since.Seconds())), workspace)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	observations := []observation{}
	queries, unlinked := 0, 0
	for rows.Next() {
		var query string
		var passagesJSON []byte
		var messageID *string
		var citationsJSON []byte
		if err := rows.Scan(&query, &passagesJSON, &messageID, &citationsJSON); err != nil {
			return nil, 0, 0, err
		}
		if messageID == nil || len(citationsJSON) == 0 {
			unlinked++
			continue
		}

		var passages []loggedPassage
		if err := json.Unmarshal(passagesJSON, &passages); err != nil || len(passages) == 0 {
			continue
		}
		var citations []struct {
			DocumentID string `json:"document_id"`
		}
		_ = json.Unmarshal(citationsJSON, &citations)
		cited := map[string]bool{}
		for _, citation := range citations {
			cited[citation.DocumentID] = true
		}

		queries++
		for _, passage := range passages {
			observations = append(observations, observation{
				Query:      query,
				Score:      passage.Score,
				DocumentID: passage.DocumentID,
				MessageID:  *messageID,
				// Matched by document rather than by passage: a citation names
				// the document, so several passages from one cited document all
				// count as used. That errs towards keeping passages, which is
				// the safe direction for choosing a floor.
				WasCited: cited[passage.DocumentID],
			})
		}
	}
	return observations, queries, unlinked, rows.Err()
}

func report(observations []observation, queries, unlinked, steps int) {
	var cited, ignored []float64
	answersUsingKnowledge := map[string]bool{}
	answersSeen := map[string]bool{}
	for _, item := range observations {
		answersSeen[item.MessageID] = true
		if item.WasCited {
			cited = append(cited, item.Score)
			answersUsingKnowledge[item.MessageID] = true
		} else {
			ignored = append(ignored, item.Score)
		}
	}

	fmt.Printf("Window: %d retrievals with an answer, %d passages.\n", queries, len(observations))
	fmt.Printf("Answers that used the knowledge base: %d of %d.\n", len(answersUsingKnowledge), len(answersSeen))
	if unlinked > 0 {
		fmt.Printf("Skipped: %d retrievals with no answer to judge them by.\n", unlinked)
	}
	fmt.Println()

	fmt.Println("Score distribution")
	fmt.Printf("  %-22s %6s %6s %6s %6s %6s  %s\n", "", "min", "p25", "median", "p75", "max", "n")
	printQuantiles("passages the answer cited", cited)
	printQuantiles("passages it ignored", ignored)
	fmt.Println()

	if len(cited) == 0 {
		fmt.Println("No answer in this window cited anything, so there is nothing to protect")
		fmt.Println("and no floor can be justified from it. Collect traffic that includes")
		fmt.Println("questions the documents do answer, then run this again.")
		return
	}

	fmt.Println("What a floor would do")
	fmt.Printf("  %-8s %14s %13s %15s %s\n", "floor", "cited kept", "cited lost", "ignored cut", "answers left empty")
	sweep, safest := sweepFloors(observations, steps)
	for _, row := range sweep {
		marker := ""
		if row.LostCited == 0 {
			marker = "  <- loses nothing an answer used"
		}
		fmt.Printf("  %-8.3f %14d %13d %15d %d%s\n", row.Floor, row.KeptCited, row.LostCited, row.CutIgnored, row.Emptied, marker)
	}

	fmt.Println()
	fmt.Printf("Highest floor that keeps every cited passage: %.3f\n", safest)
	fmt.Println("Treat that as a ceiling, not a setting. It is fitted to this window, so")
	fmt.Println("leave margin below it, and re-run after the corpus or the embedding model")
	fmt.Println("changes - the scores move with both.")
}

func printQuantiles(label string, scores []float64) {
	if len(scores) == 0 {
		fmt.Printf("  %-22s %6s %6s %6s %6s %6s  %d\n", label, "-", "-", "-", "-", "-", 0)
		return
	}
	sorted := append([]float64{}, scores...)
	sort.Float64s(sorted)
	fmt.Printf("  %-22s %6.3f %6.3f %6.3f %6.3f %6.3f  %d\n",
		label, sorted[0], quantile(sorted, 0.25), quantile(sorted, 0.5),
		quantile(sorted, 0.75), sorted[len(sorted)-1], len(sorted))
}

func quantile(sorted []float64, fraction float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := fraction * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	return sorted[lower] + (sorted[upper]-sorted[lower])*(position-float64(lower))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// floorEffect is one candidate floor and what it would have done to the window.
type floorEffect struct {
	Floor      float64
	KeptCited  int
	LostCited  int
	CutIgnored int
	// Answers that would have been left with no passage at all. A floor that
	// empties an answer which cited something has gone too far; one that empties
	// an answer which cited nothing has done exactly its job.
	Emptied int
}

// sweepFloors walks candidate floors from the lowest score seen to the highest
// and reports the cost of each, plus the highest one that loses nothing an
// answer relied on.
//
// The column that decides is LostCited. CutIgnored is the benefit and it only
// ever grows, so a floor chosen by that alone would be the highest score in the
// window - which keeps nothing at all.
func sweepFloors(observations []observation, steps int) ([]floorEffect, float64) {
	if len(observations) == 0 || steps < 1 {
		return nil, 0
	}
	answers := map[string]bool{}
	low, high := math.Inf(1), math.Inf(-1)
	for _, item := range observations {
		answers[item.MessageID] = true
		low = math.Min(low, item.Score)
		high = math.Max(high, item.Score)
	}

	effects := make([]floorEffect, 0, steps+1)
	safest := 0.0
	for step := 0; step <= steps; step++ {
		floor := low
		if high > low {
			floor = low + (high-low)*float64(step)/float64(steps)
		}
		effect := floorEffect{Floor: floor}
		survivors := map[string]bool{}
		for _, item := range observations {
			if item.Score >= floor {
				survivors[item.MessageID] = true
				if item.WasCited {
					effect.KeptCited++
				}
				continue
			}
			if item.WasCited {
				effect.LostCited++
			} else {
				effect.CutIgnored++
			}
		}
		effect.Emptied = len(answers) - len(survivors)
		if effect.LostCited == 0 {
			safest = floor
		}
		effects = append(effects, effect)
	}
	return effects, safest
}
