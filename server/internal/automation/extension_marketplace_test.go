package automation

import "testing"

func TestExtensionMarketplaceIntegrityPermissionsAndExternalVersion(t *testing.T) {
	catalog := BuiltinExtensionCatalog()
	if len(catalog) < 2 {
		t.Fatalf("expected at least 2 versioned extension releases in catalog, got %d", len(catalog))
	}
	for _, m := range catalog {
		if m.Integrity == "" {
			t.Fatalf("catalog manifest %s@%s missing integrity digest", m.ID, m.Version)
		}
		if err := ValidateExtensionManifest(m); err != nil {
			t.Fatalf("catalog manifest %s@%s failed validation: %v", m.ID, m.Version, err)
		}
	}

	// Tampering with a signed/integrity-pinned manifest must fail verification.
	tampered := catalog[0]
	tampered.Name = "Tampered Name"
	if err := ValidateExtensionManifest(tampered); err == nil {
		t.Fatal("expected tampered manifest with mismatched SHA-256 integrity to fail")
	}

	// Requesting an unapproved permission must fail verification.
	badPerm := catalog[0]
	badPerm.Integrity = ""
	badPerm.Permissions = []string{"shell.exec"}
	if err := ValidateExtensionManifest(badPerm); err == nil {
		t.Fatal("expected unapproved permission shell.exec to be rejected")
	}

	// External version range checks for Immich compatibility.
	if ok, reason := ExternalVersionCompatible(">=1.115.0 <3.0.0", "v1.118.2"); !ok {
		t.Fatalf("expected v1.118.2 to be compatible, got reason=%q", reason)
	}
	if ok, _ := ExternalVersionCompatible(">=1.115.0 <3.0.0", "1.105.0"); ok {
		t.Fatal("expected 1.105.0 to be below minimum >=1.115.0")
	}
	if ok, _ := ExternalVersionCompatible(">=1.100.0 <1.120.0", "1.125.0"); ok {
		t.Fatal("expected 1.125.0 to be rejected by <1.120.0")
	}
}
