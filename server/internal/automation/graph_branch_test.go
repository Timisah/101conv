package automation

import "testing"

func TestBranchingGraphAndBlueprints(t *testing.T) {
	blueprints := BuiltinBlueprints()
	if len(blueprints) < 6 {
		t.Fatalf("expected at least 6 built-in blueprints, got %d", len(blueprints))
	}
	for _, bp := range blueprints {
		g := GraphFromDefinition(bp.Definition)
		if err := ValidateGraph(g); err != nil {
			t.Fatalf("blueprint %q generated invalid graph: %v", bp.ID, err)
		}
		defCopy := bp.Definition
		defCopy.Graph = &g
		if err := ApplyGraphToDefinition(&defCopy); err != nil {
			t.Fatalf("blueprint %q ApplyGraphToDefinition failed: %v", bp.ID, err)
		}
		if bp.ID == "hdr-safe-media-router" {
			if defCopy.FallbackAction == nil || defCopy.FallbackAction.Mode != "skip" {
				t.Fatalf("expected hdr-safe-media-router to round-trip skip fallback action, got %+v", defCopy.FallbackAction)
			}
		}
	}

	// Schedule trigger + metadata inspect block + branching fallback.
	g := Graph{
		Nodes: []GraphNode{
			{ID: "trig", BlockID: "trigger.schedule", Version: "1.0.0", Kind: "trigger", Config: map[string]any{"inputDriveId": "drive-in", "intervalSeconds": 3600}},
			{ID: "inspect", BlockID: "condition.inspect-metadata", Version: "1.0.0", Kind: "condition", Config: map[string]any{"minWidth": 1920, "hdr": false}},
			{ID: "compress", BlockID: "action.compress", Version: "1.0.0", Kind: "action", Config: map[string]any{"mode": "compress", "targetFormat": "mp4"}},
			{ID: "skip_hdr", BlockID: "action.skip", Version: "1.0.0", Kind: "action", Config: map[string]any{"mode": "skip"}},
			{ID: "out", BlockID: "action.file-output", Version: "1.0.0", Kind: "output", Config: map[string]any{"outputDriveId": "drive-out", "behavior": "unique"}},
		},
		Edges: []GraphEdge{
			{From: "trig", To: "inspect"},
			{From: "inspect", To: "compress", Port: "true"},
			{From: "inspect", To: "skip_hdr", Port: "false"},
			{From: "compress", To: "out"},
		},
	}
	if err := ValidateGraph(g); err != nil {
		t.Fatalf("expected valid branching metadata graph, got %v", err)
	}
	var def Definition
	def.Graph = &g
	if err := ApplyGraphToDefinition(&def); err != nil {
		t.Fatalf("ApplyGraphToDefinition: %v", err)
	}
	if def.Trigger.Type != "folder.scan" || def.Trigger.IntervalSeconds != 3600 {
		t.Fatalf("expected schedule trigger to project to folder.scan@3600s, got %+v", def.Trigger)
	}
	if def.Condition == nil || !conditionNeedsMetadata(*def.Condition) {
		t.Fatalf("expected metadata condition projected from inspect-metadata block, got %+v", def.Condition)
	}
	if def.FallbackAction == nil || def.FallbackAction.Mode != "skip" {
		t.Fatalf("expected skip fallback action, got %+v", def.FallbackAction)
	}

	// Reject multiple actions without any condition/branch node.
	noCond := Graph{
		Nodes: []GraphNode{
			{ID: "trig", BlockID: "trigger.manual", Version: "1.0.0", Kind: "trigger"},
			{ID: "a1", BlockID: "action.convert", Version: "1.0.0", Kind: "action"},
			{ID: "a2", BlockID: "action.skip", Version: "1.0.0", Kind: "action"},
		},
		Edges: []GraphEdge{{From: "trig", To: "a1"}, {From: "trig", To: "a2"}},
	}
	if err := ValidateGraph(noCond); err == nil {
		t.Fatal("expected multiple actions without condition block to be rejected")
	}

	// Reject unsupported port name.
	badPort := g
	badPort.Edges = append([]GraphEdge(nil), g.Edges...)
	badPort.Edges[1].Port = "arbitrary_exec"
	if err := ValidateGraph(badPort); err == nil {
		t.Fatal("expected unsupported edge port to be rejected")
	}
}
