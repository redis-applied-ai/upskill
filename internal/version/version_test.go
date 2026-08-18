package version

import "testing"

func TestCurrent(t *testing.T) {
	if got := Current(); got != "0.1.1" {
		t.Fatalf("version = %q", got)
	}
}
