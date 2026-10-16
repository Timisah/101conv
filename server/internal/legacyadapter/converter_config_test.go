package legacyadapter

import "testing"

func TestAdaptsExistingConverterConfigToSeparateEngineAndProfile(t *testing.T) {
	data := []byte(`{
		"id":"jpeg-xl","friendlyName":"JPEG XL","priority":45,"enabled":true,
		"variables":{"executable":"/data/profiles/jpeg-xl/0.12.0/tools/cjxl","djxl":"/data/profiles/jpeg-xl/0.12.0/tools/djxl","dataDir":"/mnt/convert-data"},
		"rules":[
			{"sources":["jpg","jpeg"],"targets":["jxl"],"command":["{var:executable}","{source}","{target}","-q","85","--data-dir={var:dataDir}"]},
			{"sources":["jxl"],"targets":["png"],"command":["{var:djxl}","{source}","{target}"]}
		]
	}`)
	adapted, err := Adapt(data)
	if err != nil {
		t.Fatal(err)
	}
	if adapted.Engine.ID != "manual-legacy:jpeg-xl" || adapted.Engine.Artifact != nil || adapted.Engine.Executables["executable"] != "" {
		t.Fatalf("expected a manual engine model, got %+v", adapted.Engine)
	}
	if adapted.Installation.Managed || adapted.Installation.ExecutablePaths["executable"] != "/data/profiles/jpeg-xl/0.12.0/tools/cjxl" {
		t.Fatalf("legacy paths should remain local manual installation state: %+v", adapted.Installation)
	}
	if adapted.Installation.ExecutablePaths["dataDir"] != "" || adapted.Installation.Variables["dataDir"] != "/mnt/convert-data" {
		t.Fatalf("non-executable legacy variables should be preserved separately: %+v", adapted.Installation)
	}
	if err := adapted.Profile.Validate(); err != nil {
		t.Fatalf("adapted profile should validate: %v", err)
	}
	if len(adapted.Profile.Rules) != 2 || adapted.Profile.Rules[0].Executable != "executable" || adapted.Profile.Rules[1].Executable != "djxl" {
		t.Fatalf("legacy rules did not retain executable bindings: %+v", adapted.Profile.Rules)
	}
	if got := adapted.Profile.Rules[0].Arguments; len(got) != 5 || got[0] != "{source}" || got[1] != "{target}" || got[2] != "-q" || got[3] != "85" || got[4] != "--data-dir={engine.variable:dataDir}" {
		t.Fatalf("unexpected argument compatibility conversion: %#v", got)
	}
}

func TestAdaptsLiteralManualExecutableAndDisabledState(t *testing.T) {
	data := []byte(`{"id":"manual-copy","friendlyName":"Copy","enabled":false,"rules":[{"sources":["foo"],"targets":["bar"],"command":["/usr/bin/cp","{source}","{target}"]}]}`)
	adapted, err := Adapt(data)
	if err != nil {
		t.Fatal(err)
	}
	if adapted.ProfileState.Enabled || adapted.Installation.ExecutablePaths["default"] != "/usr/bin/cp" {
		t.Fatalf("manual executable or disabled setting not preserved: %+v", adapted)
	}
}

func TestRejectsLegacyRuleWithUnknownExecutableVariable(t *testing.T) {
	data := []byte(`{"id":"bad","rules":[{"sources":["a"],"targets":["b"],"command":["{var:missing}","{source}","{target}"]}]}`)
	if _, err := Adapt(data); err == nil {
		t.Fatal("expected missing legacy executable variable to be rejected")
	}
}
