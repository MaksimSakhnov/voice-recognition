package jobs

import "testing"

func TestEscapeLike(t *testing.T) {
	got := escapeLike(`50%_ready\now`)
	want := `50\%\_ready\\now`
	if got != want {
		t.Fatalf("escapeLike() = %q, want %q", got, want)
	}
}
