package screens

import (
	"strings"
	"testing"
)

func TestValidateScreenKeysAcceptsProcessesOverrides(t *testing.T) {
	err := ValidateScreenKeys(map[string]map[string]string{
		"processes": {"kill": "K", "sort_cpu": "C"},
	})
	if err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
}

func TestValidateScreenKeysAcceptsServicesLogsContainersOverrides(t *testing.T) {
	err := ValidateScreenKeys(map[string]map[string]string{
		"services":   {"start": "S", "sort_name": "N"},
		"logs":       {"follow": "F"},
		"containers": {"sort_state": "V", "clear_filter": "C"},
	})
	if err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
}

func TestValidateScreenKeysAcceptsStorageNetworkUsersOverrides(t *testing.T) {
	err := ValidateScreenKeys(map[string]map[string]string{
		"storage": {"toggle_view": "V", "sort_mount": "M"},
		"network": {"cycle_view": "V", "sort_local": "L"},
		"users":   {"cycle_view": "V"},
	})
	if err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
}

func TestValidateScreenKeysRejectsUnknownScreen(t *testing.T) {
	err := ValidateScreenKeys(map[string]map[string]string{
		"nope": {"kill": "K"},
	})
	if err == nil {
		t.Fatal("unknown screen must be rejected")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q must name the unknown screen", err)
	}
}

func TestValidateScreenKeysRejectsUnknownAction(t *testing.T) {
	err := ValidateScreenKeys(map[string]map[string]string{
		"processes": {"nuke": "K"},
	})
	if err == nil {
		t.Fatal("unknown action must be rejected")
	}
	if !strings.Contains(err.Error(), "nuke") {
		t.Errorf("error %q must name the unknown action", err)
	}
}

func TestValidateScreenKeysRejectsUnknownServicesAction(t *testing.T) {
	err := ValidateScreenKeys(map[string]map[string]string{
		"services": {"nuke": "K"},
	})
	if err == nil {
		t.Fatal("unknown action must be rejected")
	}
	if !strings.Contains(err.Error(), "nuke") {
		t.Errorf("error %q must name the unknown action", err)
	}
}

func TestValidateScreenKeysRejectsUnknownNetworkAction(t *testing.T) {
	err := ValidateScreenKeys(map[string]map[string]string{
		"network": {"nuke": "K"},
	})
	if err == nil {
		t.Fatal("unknown action must be rejected")
	}
	if !strings.Contains(err.Error(), "nuke") {
		t.Errorf("error %q must name the unknown action", err)
	}
}

func TestValidateScreenKeysRejectsEmptyKey(t *testing.T) {
	err := ValidateScreenKeys(map[string]map[string]string{
		"processes": {"kill": "  "},
	})
	if err == nil {
		t.Fatal("blank key must be rejected")
	}
	if !strings.Contains(err.Error(), "kill") {
		t.Errorf("error %q must name the offending action", err)
	}
}
