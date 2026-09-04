package httpapi

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"cosmo/backend/internal/knowledge"
)

var errKnowledgeIncomplete = errors.New("one or more knowledge sources could not be searched")

type knowledgeSourceStatus struct {
	KBID         string `json:"kb_id"`
	Status       string `json:"status"`
	PassageCount int    `json:"passage_count"`
	DurationMS   int64  `json:"duration_ms"`
}

type knowledgeRetrieval struct {
	Passages []knowledgePassage
	Sources  []knowledgeSourceStatus
}

func (r knowledgeRetrieval) incomplete() bool {
	for _, source := range r.Sources {
		if source.Status != "ready" && source.Status != "empty" {
			return true
		}
	}
	return false
}

type knowledgeRetrievalPolicy struct {
	workers, candidates int
	timeout, kbTimeout  time.Duration
}

func (s *Server) knowledgeRetrievalPolicy() knowledgeRetrievalPolicy {
	p := knowledgeRetrievalPolicy{s.cfg.RetrievalWorkers, s.cfg.RetrievalCandidates, s.cfg.RetrievalTimeout, s.cfg.RetrievalKBTimeout}
	if p.workers <= 0 {
		p.workers = 4
	}
	if p.workers > 16 {
		p.workers = 16
	}
	if p.candidates <= 0 {
		p.candidates = 24
	}
	if p.candidates > 100 {
		p.candidates = 100
	}
	if p.timeout <= 0 {
		p.timeout = 30 * time.Second
	}
	if p.kbTimeout <= 0 {
		p.kbTimeout = 10 * time.Second
	}
	return p
}

// fanOutKnowledge bounds active work rather than spawning a goroutine per KB.
// Waiting for a slot consumes the total deadline, but not the per-KB timeout.
// Each worker owns a distinct result slot; completion order cannot alter fusion.
func fanOutKnowledge(ctx context.Context, ids []string, policy knowledgeRetrievalPolicy, search func(context.Context, string) ([]knowledge.Passage, error)) ([][]knowledge.Passage, []knowledgeSourceStatus) {
	lists := make([][]knowledge.Passage, len(ids))
	states := make([]knowledgeSourceStatus, len(ids))
	jobs := make(chan int, len(ids))
	for i := range ids {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for n := 0; n < min(policy.workers, len(ids)); n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				start := time.Now()
				kbCtx, cancel := context.WithTimeout(ctx, policy.kbTimeout)
				var found []knowledge.Passage
				err := kbCtx.Err()
				if err == nil {
					found, err = search(kbCtx, ids[i])
				}
				// A request that exhausted its deadline must not become a successful
				// empty response just because a dependency ignored cancellation.
				if err == nil {
					err = kbCtx.Err()
				}
				cancel()
				state := knowledgeSourceStatus{KBID: ids[i], Status: "empty", DurationMS: time.Since(start).Milliseconds()}
				if err != nil {
					state.Status = "failed"
					if errors.Is(err, context.DeadlineExceeded) {
						state.Status = "timed_out"
					}
					if errors.Is(err, context.Canceled) {
						state.Status = "canceled"
					}
				} else {
					for _, passage := range found {
						// Check the individual request boundary before logging/fusion.
						if passage.KBID != ids[i] {
							state.Status = "failed"
							continue
						}
						if strings.TrimSpace(passage.Text) != "" && len(lists[i]) < policy.candidates {
							lists[i] = append(lists[i], passage)
						}
					}
					state.PassageCount = len(lists[i])
					if state.PassageCount > 0 && state.Status != "failed" {
						state.Status = "ready"
					}
				}
				states[i] = state
			}
		}()
	}
	workers.Wait()
	return lists, states
}
