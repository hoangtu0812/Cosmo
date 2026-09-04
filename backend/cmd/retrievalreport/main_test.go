package main

import "testing"

// Two answers. One asked something the documents cover and cited the passage
// that scored 0.81; the other asked something unrelated and cited nothing, but
// retrieval still handed it three passages, because nearest is never none.
func observations() []observation {
	return []observation{
		{MessageID: "msg_relevant", Score: 0.81, WasCited: true, DocumentID: "doc_a"},
		{MessageID: "msg_relevant", Score: 0.44, WasCited: false, DocumentID: "doc_b"},
		{MessageID: "msg_unrelated", Score: 0.29, WasCited: false, DocumentID: "doc_c"},
		{MessageID: "msg_unrelated", Score: 0.22, WasCited: false, DocumentID: "doc_d"},
		{MessageID: "msg_unrelated", Score: 0.18, WasCited: false, DocumentID: "doc_e"},
	}
}

// The number the report exists to produce: the highest floor that still keeps
// every passage an answer relied on. Above 0.81 the cited passage would be cut,
// so 0.81 is the ceiling and anything higher is a regression nobody sees until
// answers start missing their sources.
func TestSweepFloorsStopsBeforeLosingACitedPassage(t *testing.T) {
	_, safest := sweepFloors(observations(), 100)

	if safest > 0.81 {
		t.Fatalf("floor %.3f would cut the passage the answer cited", safest)
	}
	if safest < 0.7 {
		t.Fatalf("floor %.3f is far below what the evidence allows", safest)
	}
}

// A floor is only worth setting if it removes the passages nobody used. This is
// the benefit side, and it is what makes the unrelated answer stop citing.
func TestSweepFloorsCutsWhatNoAnswerUsed(t *testing.T) {
	sweep, safest := sweepFloors(observations(), 100)

	var atSafest floorEffect
	for _, row := range sweep {
		if row.Floor <= safest {
			atSafest = row
		}
	}
	if atSafest.LostCited != 0 {
		t.Fatalf("the safest floor lost %d cited passages", atSafest.LostCited)
	}
	if atSafest.CutIgnored != 4 {
		t.Fatalf("cut %d unused passages, want all 4", atSafest.CutIgnored)
	}
	// The unrelated answer keeps nothing, which is the whole point; the
	// relevant one keeps what it cited.
	if atSafest.Emptied != 1 {
		t.Fatalf("emptied %d answers, want only the one that cited nothing", atSafest.Emptied)
	}
}

// A window where every passage scores the same has no floor to find, and the
// sweep must not divide by that zero range or report a floor it cannot support.
func TestSweepFloorsHandlesAFlatWindow(t *testing.T) {
	flat := []observation{
		{MessageID: "msg_a", Score: 0.5, WasCited: true},
		{MessageID: "msg_b", Score: 0.5, WasCited: false},
	}
	sweep, safest := sweepFloors(flat, 10)
	if len(sweep) == 0 {
		t.Fatal("a flat window produced no rows")
	}
	if safest != 0.5 {
		t.Fatalf("safest floor %.3f, want the one score present", safest)
	}
	for _, row := range sweep {
		if row.LostCited != 0 {
			t.Fatalf("a floor at or below the only score cannot lose anything: %+v", row)
		}
	}
}

func TestSweepFloorsSaysNothingWithoutObservations(t *testing.T) {
	sweep, safest := sweepFloors(nil, 10)
	if sweep != nil || safest != 0 {
		t.Fatalf("an empty window should yield no recommendation: %v %v", sweep, safest)
	}
}
