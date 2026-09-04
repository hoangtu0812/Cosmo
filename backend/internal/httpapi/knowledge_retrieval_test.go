package httpapi

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"cosmo/backend/internal/knowledge"
)

func TestKnowledgeFanOutBoundsActiveSearches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var active, peak atomic.Int32
	started, release := make(chan struct{}, 8), make(chan struct{})
	done := make(chan []knowledgeSourceStatus, 1)
	go func() {
		_, states := fanOutKnowledge(ctx, []string{"a", "b", "c", "d", "e", "f"}, knowledgeRetrievalPolicy{workers: 2, candidates: 4, kbTimeout: time.Second}, func(ctx context.Context, id string) ([]knowledge.Passage, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			started <- struct{}{}
			select {
			case <-release:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		done <- states
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("searches did not run concurrently")
		}
	}
	close(release)
	states := <-done
	if peak.Load() != 2 || active.Load() != 0 {
		t.Fatalf("invalid active search count: peak=%d active=%d", peak.Load(), active.Load())
	}
	for _, state := range states {
		if state.Status != "empty" {
			t.Fatalf("unexpected state: %+v", state)
		}
	}
}

func TestKnowledgeFanOutKeepsEvidenceOnFailureAndTimeout(t *testing.T) {
	policy := knowledgeRetrievalPolicy{workers: 3, candidates: 2, kbTimeout: 50 * time.Millisecond}
	lists, states := fanOutKnowledge(context.Background(), []string{"ready", "failed", "slow", "empty", "wrong"}, policy, func(ctx context.Context, id string) ([]knowledge.Passage, error) {
		switch id {
		case "failed":
			return nil, errors.New("private upstream details")
		case "slow":
			<-ctx.Done()
			return nil, ctx.Err()
		case "empty":
			return nil, nil
		case "wrong":
			return []knowledge.Passage{{KBID: "outside-allow-list", Text: "secret"}}, nil
		default:
			return []knowledge.Passage{{KBID: id, Text: "valid"}, {KBID: id, Text: "second"}, {KBID: id, Text: "over budget"}}, nil
		}
	})
	want := []string{"ready", "failed", "timed_out", "empty", "failed"}
	for i, state := range states {
		if state.Status != want[i] {
			t.Fatalf("%s: got %s want %s", state.KBID, state.Status, want[i])
		}
	}
	if len(lists[0]) != 2 || len(fuseKnowledgeRanks(lists, 2)) != 2 || len(lists[4]) != 0 {
		t.Fatalf("lost valid evidence or leaked unauthorised results: %+v", lists)
	}
}

func TestKnowledgeFanOutCancellationSkipsQueuedCalls(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if timeout {
			cancel()
			ctx, cancel = context.WithTimeout(context.Background(), 40*time.Millisecond)
		}
		var calls atomic.Int32
		_, states := fanOutKnowledge(ctx, []string{"a", "b", "c", "d"}, knowledgeRetrievalPolicy{workers: 1, candidates: 2, kbTimeout: time.Second}, func(ctx context.Context, id string) ([]knowledge.Passage, error) {
			calls.Add(1)
			if !timeout {
				cancel()
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})
		cancel()
		if calls.Load() != 1 {
			t.Fatalf("started queued work after cancellation: %d", calls.Load())
		}
		want := "canceled"
		if timeout {
			want = "timed_out"
		}
		for _, state := range states {
			if state.Status != want {
				t.Fatalf("invalid terminal status: %+v", state)
			}
		}
	}
}
