package read

import (
	"strings"
	"testing"
)

func TestVersionTokenUsesSixCrockfordBase32Characters(t *testing.T) {
	version := HashVersion([]byte("versioned content"))
	if len(version) != 6 {
		t.Fatalf("HashVersion length = %d, want 6 (%q)", len(version), version)
	}
	if err := ValidateVersion(version); err != nil {
		t.Fatalf("ValidateVersion(%q) returned error: %v", version, err)
	}
	if err := ValidateVersion(strings.ToUpper(version)); err != nil {
		t.Fatalf("ValidateVersion must accept uppercase token: %v", err)
	}
}

func TestValidateVersionRejectsLegacyTwelveCharacterToken(t *testing.T) {
	if err := ValidateVersion("9k3m7x2p4t6w"); err == nil {
		t.Fatal("ValidateVersion accepted legacy 12-character token")
	}
}
