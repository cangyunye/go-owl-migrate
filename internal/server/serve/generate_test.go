package serve

import "testing"

func TestRandSuffixUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		s := randSuffix()
		if s == "" {
			t.Fatal("randSuffix returned empty string")
		}
		if seen[s] {
			t.Fatalf("randSuffix collided after %d calls: %q", i, s)
		}
		seen[s] = true
	}
}
