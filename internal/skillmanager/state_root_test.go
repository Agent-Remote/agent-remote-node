package skillmanager

import "testing"

func TestStateRootRejectsCleanupAndAccountOverlap(t *testing.T) {
	for _, root := range []string{"/", "relative", "/srv/a/../skills", "/srv/accounts", "/srv/accounts/skills", "/srv"} {
		if err := ValidateStateRoot(root, "/srv/accounts"); err == nil {
			t.Fatalf("unsafe state root accepted: %s", root)
		}
	}
	if err := ValidateStateRoot("/srv/skills", "/srv/accounts", "/srv/sessions"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStateRoot("/srv/skills", "/"); err == nil {
		t.Fatal("root-wide shared path overlaps skill state")
	}
}

func TestStatePolicyRequiresExplicitPositiveQuotas(t *testing.T) {
	policy := DefaultStatePolicy()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if policy.CheckpointBytes != 1<<30 || policy.DirectoryBytes != 10<<30 || policy.MinimumFreeBytes != 2<<30 || policy.ReservePercent != 5 {
		t.Fatal("published defaults drifted")
	}
	for _, mutate := range []func(*StatePolicy){
		func(p *StatePolicy) { p.CheckpointBytes = 0 }, func(p *StatePolicy) { p.DirectoryBytes = 1 },
		func(p *StatePolicy) { p.Entries = 100_001 }, func(p *StatePolicy) { p.MinimumFreeBytes = 0 },
		func(p *StatePolicy) { p.ReservePercent = 101 },
	} {
		invalid := policy
		mutate(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatal("invalid state policy accepted")
		}
	}
}
