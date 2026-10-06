package collectors_test

import (
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
)

func TestHardwareDataFixture(t *testing.T) {
	sys := collectors.New("testdata")
	hw, err := sys.HardwareData()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hw.Vendor != "Dell Inc." || hw.Model != "PowerEdge R240" {
		t.Errorf("hardware = %+v, want Dell Inc. / PowerEdge R240", hw)
	}
}

func TestHardwareDataMissingDmidDegradesGracefuly(t *testing.T) {
	// No DMI tree at all: VMs and ARM boards look like this in production.
	sys := collectors.New(t.TempDir())
	hw, err := sys.HardwareData()
	if err != nil {
		t.Fatalf("missing DMI must not error, got %v", err)
	}
	if hw.Vendor != "Unknown" || hw.Model != "Unknown" {
		t.Errorf("hardware = %+v, want Unknown/Unknown", hw)
	}
}
