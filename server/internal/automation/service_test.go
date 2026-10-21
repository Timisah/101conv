package automation

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestResolveDrivePathContainsPathsAndRejectsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "input")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	s := &Service{allowedRoot: []string{root}}
	if got, err := s.ResolveDrivePath(inside); err != nil || got != inside {
		t.Fatalf("resolve approved directory = %q, %v", got, err)
	}
	if _, err := s.ResolveDrivePath(outside); err == nil {
		t.Fatal("accepted directory outside configured root")
	}
	if _, err := s.ResolveDrivePath(filepath.Join(root, "..", filepath.Base(outside))); err == nil {
		t.Fatal("accepted traversal outside configured root")
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveDrivePath(link); err == nil {
		t.Fatal("accepted symlinked directory")
	}
}

func TestConditionTreeEvaluationAndBounds(t *testing.T) {
	c := Condition{Operator: "and", Children: []Condition{
		{Operator: "or", Children: []Condition{{Operator: "extension", Value: "jpg"}, {Operator: "extension", Value: ".png"}}},
		{Operator: "not", Children: []Condition{{Operator: "name_glob", Value: "*_private.*"}}},
		{Operator: "min_size", Bytes: 100},
	}}
	if err := validateCondition(c, 0, new(int)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := evaluateCondition(c, "summer.JPG", 200); !ok {
		t.Fatal("expected matching file")
	}
	if ok, _ := evaluateCondition(c, "summer_private.jpg", 200); ok {
		t.Fatal("NOT child should exclude private filename")
	}
	if ok, reason := matchesDefinition(Definition{Condition: &c}, "summer.jpg", 50); ok || reason == "" {
		t.Fatal("expected below-minimum condition to be rejected with a reason")
	}
	if err := validateCondition(Condition{Operator: "run_shell", Value: "anything"}, 0, new(int)); err == nil {
		t.Fatal("accepted unknown/executable condition")
	}
}

func TestStagePublicationValidatesContentsAndReceipt(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	digest, err := stagePublication(root, "receipt", strings.NewReader("converted"), 9)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := root.ReadFile("receipt"); err != nil || !bytes.Equal(got, []byte("converted")) {
		t.Fatalf("receipt=%q %v", got, err)
	}
	if repeated, err := stagePublication(root, "receipt", strings.NewReader("converted"), 9); err != nil || repeated != digest {
		t.Fatalf("recovery digest=%q %v", repeated, err)
	}
	if _, err = stagePublication(root, "receipt", strings.NewReader("different"), 9); err == nil {
		t.Fatal("adopted changed receipt")
	}
	if _, err = stagePublication(root, "invalid", strings.NewReader("short"), 9); err == nil {
		t.Fatal("accepted incorrect output size")
	}
	if err = root.Link("receipt", "output"); err != nil {
		t.Fatal(err)
	}
	if !samePublication(root, "receipt", "output", digest) {
		t.Fatal("did not recognize own publication")
	}
	if err = root.WriteFile("foreign", []byte("converted"), 0600); err != nil {
		t.Fatal(err)
	}
	if samePublication(root, "receipt", "foreign", digest) {
		t.Fatal("adopted an equal-content foreign file")
	}
}

func TestExtensionAPIRangeSelection(t *testing.T) {
	manifest := ExtensionManifest{ID: "example", Publisher: "publisher", Version: "2.1.0", ExtensionAPIRange: APIVersionRange{Minimum: "1.0.0", MaximumExclusive: "2.0.0"}}
	if !ExtensionCompatible(manifest, "1.0.0") || !ExtensionCompatible(manifest, "1.9.9") {
		t.Fatal("compatible API versions were rejected")
	}
	if ExtensionCompatible(manifest, "2.0.0") || ExtensionCompatible(manifest, "0.9.0") {
		t.Fatal("incompatible API version was accepted")
	}
}

func TestSelectCompatibleExtensionDoesNotHideOlderRelease(t *testing.T) {
	compatible := ExtensionManifest{ID: "immich", Publisher: "101conv", ExtensionAPIRange: APIVersionRange{Minimum: "1.0.0", MaximumExclusive: "2.0.0"}}
	versions := []ExtensionManifest{
		{ID: compatible.ID, Publisher: compatible.Publisher, Version: "1.4.0", ExtensionAPIRange: compatible.ExtensionAPIRange},
		{ID: compatible.ID, Publisher: compatible.Publisher, Version: "2.0.0", ExtensionAPIRange: APIVersionRange{Minimum: "2.0.0", MaximumExclusive: "3.0.0"}},
		{ID: compatible.ID, Publisher: compatible.Publisher, Version: "1.9.0", ExtensionAPIRange: compatible.ExtensionAPIRange},
	}
	selected, ok := SelectCompatibleExtension(versions, "1.5.0")
	if !ok || selected.Version != "1.9.0" {
		t.Fatalf("compatible release selection = %+v, %v; want newest compatible 1.9.0", selected, ok)
	}
	if _, ok = SelectCompatibleExtension(versions, "3.0.0"); ok {
		t.Fatal("selected a package outside the declared API range")
	}
}

func TestVisualGraphValidationAndLegacyProjection(t *testing.T) {
	d := Definition{
		Trigger: Trigger{Type: "folder.scan", InputDriveID: "incoming", IntervalSeconds: 60, StabilitySeconds: 30, MaxConcurrent: 1},
		Action:  FileAction{Mode: "convert", ProfileID: "png-jpg", ProfileVersion: "1.0.0", TargetFormat: "jpg", OutputDriveID: "out", OutputBehavior: "unique"},
		Filter:  FileFilter{Extensions: []string{"png"}},
	}
	graph := GraphFromDefinition(d)
	if err := ValidateGraph(graph); err != nil {
		t.Fatal(err)
	}
	projected := Definition{Graph: &graph}
	if err := ApplyGraphToDefinition(&projected); err != nil {
		t.Fatal(err)
	}
	if projected.Trigger.InputDriveID != "incoming" || projected.Action.OutputDriveID != "out" || projected.Filter.Extensions[0] != "png" {
		t.Fatalf("graph projection lost execution fields: %+v", projected)
	}
	cycle := graph
	cycle.Edges = append(cycle.Edges, GraphEdge{From: "output", To: "trigger"})
	if err := ValidateGraph(cycle); err == nil {
		t.Fatal("accepted a cyclic visual graph")
	}
}

func TestExtensionManifestValidationRemainsDeclarative(t *testing.T) {
	manifest := ExtensionManifest{ID: "example", Name: "Example", Publisher: "owner", Version: "1.0.0", ExtensionAPIRange: APIVersionRange{Minimum: "1.0.0", MaximumExclusive: "2.0.0"}, ConfigurationSchema: 1, Blocks: []Block{{ID: "example.condition", Version: "1.0.0", Kind: "condition", Name: "Match", ConfigSchema: map[string]any{"value": "string"}}}}
	if err := ValidateExtensionManifest(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Blocks[0].ConfigSchema["command"] = "string"
	if err := ValidateExtensionManifest(manifest); err == nil {
		t.Fatal("accepted executable extension configuration")
	}
}

func TestAutomationMigrationsAreIdempotentAndDriveRecordsPersist(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("101CONV_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL automation persistence tests")
	}
	ctx := context.Background()
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("automation_%d", time.Now().UnixNano())
	if _, err = adminDB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer adminDB.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	address, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := address.Query()
	query.Set("search_path", schema)
	address.RawQuery = query.Encode()
	db, err := sql.Open("pgx", address.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	root := t.TempDir()
	input := filepath.Join(root, "input")
	if err = os.Mkdir(input, 0700); err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(db, []string{root}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = svc.Migrate(ctx); err != nil {
		t.Fatalf("second migration was not idempotent: %v", err)
	}
	drive, err := svc.CreateDrive(ctx, "Incoming", input, "read")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewService(db, []string{root}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := restarted.CheckDrive(ctx, drive.ID)
	if err != nil || got.Name != "Incoming" || !got.Accessible {
		t.Fatalf("drive did not persist: %+v %v", got, err)
	}
}
