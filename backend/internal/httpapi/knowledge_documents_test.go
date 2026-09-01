package httpapi

import (
	"strings"
	"testing"
)

func TestBuildGroundingPromptIsEmptyWithoutPassages(t *testing.T) {
	// No passages must mean no system message at all. An empty grounding
	// block would still tell the model to answer only from sources it does
	// not have, which turns every ungrounded question into a refusal.
	if got := buildGroundingPrompt(nil); got != "" {
		t.Fatalf("expected an empty prompt, got %q", got)
	}
}

func TestBuildGroundingPromptNumbersAndLabelsPassages(t *testing.T) {
	prompt := buildGroundingPrompt([]knowledgePassage{
		{Title: "Operating Procedure", Section: "Startup", Page: "12", Text: "Open the suction valve first."},
		{Title: "Lessons Learned", Text: "P-101 tripped on high vibration."},
	})

	for _, want := range []string{
		"[1] Operating Procedure — Startup — tr. 12",
		"Open the suction valve first.",
		"[2] Lessons Learned",
		"P-101 tripped on high vibration.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q\n---\n%s", want, prompt)
		}
	}
}

func TestBuildGroundingPromptForbidsFillingGaps(t *testing.T) {
	// The instruction not to answer beyond the passages is the whole point of
	// grounding; losing it would leave retrieval as decoration.
	prompt := buildGroundingPrompt([]knowledgePassage{{Title: "Manual", Text: "Torque to 40 Nm."}})
	if !strings.Contains(strings.ToLower(prompt), "do not contain the answer") {
		t.Errorf("prompt does not tell the model what to do when the passages fall short:\n%s", prompt)
	}
}

func TestBuildGroundingPromptAvoidsRepeatedBulletCitations(t *testing.T) {
	prompt := buildGroundingPrompt([]knowledgePassage{{Title: "Manual", Text: "Torque to 40 Nm."}})
	if !strings.Contains(prompt, "Do not repeat the same citation after every bullet") {
		t.Errorf("prompt does not request compact citations:\n%s", prompt)
	}
}

func TestPassageLabelOmitsMissingParts(t *testing.T) {
	cases := map[string]knowledgePassage{
		"Manual":                  {Title: "Manual"},
		"Manual — Safety":         {Title: "Manual", Section: "Safety"},
		"Manual — tr. 3":          {Title: "Manual", Page: "3"},
		"Manual — Safety — tr. 3": {Title: "Manual", Section: "Safety", Page: "3"},
	}
	for want, passage := range cases {
		if got := passage.label(); got != want {
			t.Errorf("label() = %q, want %q", got, want)
		}
	}
}

func TestDocumentExtensionsRejectExecutables(t *testing.T) {
	// The allow-list exists so a file the ingestion service cannot read is
	// refused on upload rather than accepted and left permanently unusable.
	for _, extension := range []string{".exe", ".sh", ".zip", ".png", ""} {
		if documentExtensions[extension] {
			t.Errorf("%q should not be an accepted document extension", extension)
		}
	}
	for _, extension := range []string{".pdf", ".docx", ".md", ".txt"} {
		if !documentExtensions[extension] {
			t.Errorf("%q should be an accepted document extension", extension)
		}
	}
}
