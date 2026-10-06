package collectors

import (
	"github.com/felipecastillo-b/serverctl/internal/core"
)

// HardwareData reads the machine vendor and model from DMI sysfs.
// Missing DMI data is NORMAL (VMs and many ARM boards do not expose it),
// so absence degrades to "Unknown" fields without an error.
func (s System) HardwareData() (core.HardwareInfo, error) {
	vendor, err := readTrimmed(s.sys("devices", "virtual", "dmi", "id", "sys_vendor"))
	if err != nil {
		vendor = "Unknown"
	}
	model, err := readTrimmed(s.sys("devices", "virtual", "dmi", "id", "product_name"))
	if err != nil {
		model = "Unknown"
	}
	return core.HardwareInfo{Vendor: vendor, Model: model}, nil
}
