package version

import "testing"

func TestCurrent(t *testing.T) {
	if got := Current(); got != "dev" {
		t.Fatalf("version = %q", got)
	}

	value = "v1.2.3"
	t.Cleanup(func() { value = "dev" })
	if got := Current(); got != "v1.2.3" {
		t.Fatalf("injected version = %q", got)
	}
	if got := Semver(); got != "1.2.3" {
		t.Fatalf("semantic version = %q", got)
	}
}
