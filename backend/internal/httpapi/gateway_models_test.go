package httpapi

import "testing"

func boolPointer(value bool) *bool { return &value }

func TestReasoningEffortsUseExplicitGatewayLevels(t *testing.T) {
	got := reasoningEffortsFor(boolPointer(true), []string{"none", "low", "high", "xhigh"}, nil, nil, nil, nil, nil, nil)
	want := []string{"none", "low", "high", "xhigh"}
	if len(got) != len(want) {
		t.Fatalf("efforts = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("efforts = %v, want %v", got, want)
		}
	}
}

func TestReasoningEffortsStayHiddenWithoutCapability(t *testing.T) {
	if got := reasoningEffortsFor(boolPointer(false), nil, []string{"temperature"}, nil, nil, nil, nil, nil); len(got) != 0 {
		t.Fatalf("non-reasoning model got efforts %v", got)
	}
}

func TestReasoningEffortsBuildConservativeFallback(t *testing.T) {
	got := reasoningEffortsFor(nil, nil, []string{"reasoning_effort"}, boolPointer(true), boolPointer(true), boolPointer(false), boolPointer(true), boolPointer(true))
	want := []string{"none", "minimal", "medium", "high", "xhigh", "max"}
	if len(got) != len(want) {
		t.Fatalf("efforts = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("efforts = %v, want %v", got, want)
		}
	}
}

func TestDescribeGatewayModelsFiltersNonChatModes(t *testing.T) {
	metadata := map[string]gatewayModelMetadata{
		"chat-model":  {Mode: "chat", Provider: "openai"},
		"embed-model": {Mode: "embedding"},
		"reranker":    {Mode: "rerank"},
	}
	got := describeGatewayModels([]string{"chat-model", "embed-model", "reranker", "unknown"}, metadata, true)
	if len(got) != 2 || got[0].ID != "chat-model" || got[1].ID != "unknown" {
		t.Fatalf("chat models = %#v", got)
	}
}
