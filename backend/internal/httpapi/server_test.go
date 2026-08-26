package httpapi

import "testing"

func TestValidPassword(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"StrongPass1", true},
		{"mậtkhẩumạnh2", true},
		{"short1", false},
		{"onlyletters", false},
		{"12345678901", false},
	}
	for _, test := range tests {
		if got := validPassword(test.value); got != test.want {
			t.Fatalf("validPassword(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestValidEmail(t *testing.T) {
	if !validEmail("admin@cosmo.local") {
		t.Fatal("expected valid email")
	}
	if validEmail("not-an-email") {
		t.Fatal("expected invalid email")
	}
}
