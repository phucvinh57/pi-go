package auth

import (
	"slices"
	"testing"
)

func TestPrimaryLeavesOutOptionalProviders(t *testing.T) {
	if !slices.Contains(Supported(), "laya") {
		t.Fatal("laya must stay supported, so --provider laya works")
	}
	if slices.Contains(Primary(), "laya") {
		t.Errorf("Primary() = %v: laya is optional and must not be offered or checked by default", Primary())
	}
	if got, want := len(LoginMethods()), len(Primary()); got != want {
		t.Errorf("%d login methods for %d providers", got, want)
	}
}
