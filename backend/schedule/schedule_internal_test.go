package schedule

import "testing"

// TestSafeParseCron_RecoversFromUnderlyingPanic exercises safeParseCron
// directly (bypassing Config.Validate's "=" guard) to prove the recover
// itself works, independent of whichever guard currently keeps panicking
// input from reaching it. "TZ=" triggers a real slice-bounds panic in
// robfig/cron/v3 v3.0.1 (parser.go:99: spec[eq+1:i] with i == -1).
func TestSafeParseCron_RecoversFromUnderlyingPanic(t *testing.T) {
	err := safeParseCron("TZ=")
	if err == nil {
		t.Fatal("safeParseCron(\"TZ=\") = nil error, want an error (the underlying parser panics on this input)")
	}
}

func TestSafeParseCron_ValidExpression(t *testing.T) {
	if err := safeParseCron("0 4 * * *"); err != nil {
		t.Fatalf("safeParseCron() error = %v, want nil", err)
	}
}
