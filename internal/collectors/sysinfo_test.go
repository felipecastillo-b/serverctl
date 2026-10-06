package collectors_test

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
)

func TestLoadAvgFixture(t *testing.T) {
	sys := collectors.New("testdata")
	load, err := sys.LoadAvg()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if load.Load1 != 0.42 || load.Load5 != 0.36 || load.Load15 != 0.30 {
		t.Errorf("load = %+v, want 0.42/0.36/0.30", load)
	}
}

func TestUptimeFixture(t *testing.T) {
	sys := collectors.New("testdata")
	up, err := sys.Uptime()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Duration(123456.78 * float64(time.Second))
	if up != want {
		t.Errorf("uptime = %v, want %v", up, want)
	}
}

func TestKernelFixture(t *testing.T) {
	sys := collectors.New("testdata")
	kernel, err := sys.Kernel()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kernel.Release != "6.11.5-arch1-1" {
		t.Errorf("release = %q", kernel.Release)
	}
	if !strings.HasPrefix(kernel.Version, "Linux version 6.11.5-arch1-1") {
		t.Errorf("version = %q", kernel.Version)
	}
	if kernel.Arch != runtime.GOARCH {
		t.Errorf("arch = %q, want host GOARCH %q", kernel.Arch, runtime.GOARCH)
	}
	// The fixture DMI provides a model the kernel info surfaces.
	if kernel.HardwareModel != "PowerEdge R240" {
		t.Errorf("hardware model = %q", kernel.HardwareModel)
	}
}

func TestHostnameFixture(t *testing.T) {
	sys := collectors.New("testdata")
	host, err := sys.Hostname()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "buildbox" {
		t.Errorf("hostname = %q, want %q", host, "buildbox")
	}
}
