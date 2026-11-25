package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var graphIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// GraphFromDefinition creates the editor representation for legacy and new
// definitions alike. Keeping this conversion deterministic makes the API safe
// for clients that do not yet understand graph fields.
func GraphFromDefinition(d Definition) Graph {
	nodes := []GraphNode{
		{ID: "trigger", BlockID: triggerBlockID(d.Trigger.Type), Version: "1.0.0", Kind: "trigger", Config: map[string]any{
			"type": d.Trigger.Type, "inputDriveId": d.Trigger.InputDriveID, "intervalSeconds": d.Trigger.IntervalSeconds,
			"stabilitySeconds": d.Trigger.StabilitySeconds, "maxConcurrent": d.Trigger.MaxConcurrent,
			"processExistingOnNextScan": d.Trigger.ProcessExistingOnNext,
		}},
	}
	conditionConfig := map[string]any{
		"extensions": d.Filter.Extensions, "nameGlob": d.Filter.NameGlob, "minBytes": d.Filter.MinBytes, "maxBytes": d.Filter.MaxBytes,
	}
	if d.Condition != nil {
		conditionConfig["tree"] = d.Condition
	}
	hasCondition := len(d.Filter.Extensions) > 0 || d.Filter.NameGlob != "" || d.Filter.MinBytes > 0 || d.Filter.MaxBytes > 0 || d.Condition != nil
	condID := ""
	if hasCondition {
		condID = "conditions"
		condBlock := "condition.file-filter"
		if d.FallbackAction != nil {
			condBlock = "condition.branch"
		} else if d.Condition != nil && conditionNeedsMetadata(*d.Condition) {
			condBlock = "condition.inspect-metadata"
		}
		nodes = append(nodes, GraphNode{ID: condID, BlockID: condBlock, Version: "1.0.0", Kind: "condition", Config: conditionConfig})
	}
	mode := d.Action.Mode
	blockID := "action.convert"
	if mode == "compress" {
		blockID = "action.compress"
	} else if mode == "build" {
		blockID = "action.build"
	}
	nodes = append(nodes, GraphNode{ID: "action", BlockID: blockID, Version: "1.0.0", Kind: "action", Config: map[string]any{
		"mode": mode, "profileId": d.Action.ProfileID, "profileVersion": d.Action.ProfileVersion,
		"targetFormat": d.Action.TargetFormat, "options": d.Action.Options,
	}})
	nodes = append(nodes, GraphNode{ID: "output", BlockID: "action.file-output", Version: "1.0.0", Kind: "output", Config: map[string]any{
		"outputDriveId": d.Action.OutputDriveID, "behavior": d.Action.OutputBehavior,
	}})
	edges := make([]GraphEdge, 0, 5)
	if condID != "" {
		edges = append(edges, GraphEdge{From: "trigger", To: condID})
		if d.FallbackAction != nil {
			edges = append(edges, GraphEdge{From: condID, To: "action", Port: "true"})
		} else {
			edges = append(edges, GraphEdge{From: condID, To: "action"})
		}
	} else {
		edges = append(edges, GraphEdge{From: "trigger", To: "action"})
	}
	edges = append(edges, GraphEdge{From: "action", To: "output"})

	if d.FallbackAction != nil && condID != "" {
		fbMode := d.FallbackAction.Mode
		fbBlock := "action.skip"
		switch fbMode {
		case "stop":
			fbBlock = "action.stop"
		case "convert":
			fbBlock = "action.convert"
		case "compress":
			fbBlock = "action.compress"
		}
		nodes = append(nodes, GraphNode{
			ID:      "fallback",
			BlockID: fbBlock,
			Version: "1.0.0",
			Kind:    "action",
			Config: map[string]any{
				"mode":           fbMode,
				"profileId":      d.FallbackAction.ProfileID,
				"profileVersion": d.FallbackAction.ProfileVersion,
				"targetFormat":   d.FallbackAction.TargetFormat,
				"options":        d.FallbackAction.Options,
			},
		})
		edges = append(edges, GraphEdge{From: condID, To: "fallback", Port: "false"})
	}
	return Graph{Nodes: nodes, Edges: edges}
}

func triggerBlockID(trigger string) string {
	if trigger == "manual" {
		return "trigger.manual"
	}
	return "trigger.folder-scan"
}

// ValidateGraph checks the bounded, typed graph contract without consulting
// mutable installations. Extension availability is checked when a revision is
// activated; the graph itself remains readable for rollback and repair.
func ValidateGraph(g Graph) error {
	if len(g.Nodes) == 0 || len(g.Nodes) > 32 {
		return errors.New("visual workflow must contain between 1 and 32 blocks")
	}
	if len(g.Edges) > 64 {
		return errors.New("visual workflow has too many connections")
	}
	nodes := make(map[string]GraphNode, len(g.Nodes))
	triggerCount, actionCount, conditionCount := 0, 0, 0
	for _, node := range g.Nodes {
		if !graphIDPattern.MatchString(node.ID) || node.BlockID == "" || len(node.BlockID) > 128 || len(node.Version) > 40 {
			return fmt.Errorf("visual workflow contains an invalid block identifier")
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("visual workflow contains duplicate block %q", node.ID)
		}
		if node.Kind != "trigger" && node.Kind != "condition" && node.Kind != "action" && node.Kind != "output" {
			return fmt.Errorf("unsupported visual block kind %q", node.Kind)
		}
		if node.ExtensionID == "" {
			if expected, ok := builtinBlockKind(node.BlockID); ok && expected != node.Kind {
				return fmt.Errorf("block %q has kind %q; expected %q", node.BlockID, node.Kind, expected)
			}
			if !okBuiltinBlock(node.BlockID) {
				return fmt.Errorf("unknown built-in block %q", node.BlockID)
			}
		} else if !graphIDPattern.MatchString(node.ExtensionID) || node.ExtensionVersion == "" {
			return fmt.Errorf("extension block %q must pin an extension release", node.BlockID)
		}
		if len(node.Config) > 32 {
			return fmt.Errorf("block %q has too many configuration fields", node.ID)
		}
		for key := range node.Config {
			key = strings.ToLower(key)
			if key == "command" || key == "shell" || key == "argv" || key == "executable" || key == "source" {
				return fmt.Errorf("block %q contains executable configuration", node.ID)
			}
		}
		nodes[node.ID] = node
		switch node.Kind {
		case "trigger":
			triggerCount++
		case "condition":
			conditionCount++
		case "action":
			actionCount++
		}
	}
	if triggerCount != 1 {
		return errors.New("visual workflow must contain exactly one trigger block")
	}
	if actionCount < 1 || actionCount > 4 {
		return errors.New("visual workflow must contain between 1 and 4 processing actions")
	}
	if actionCount > 1 && conditionCount == 0 {
		return errors.New("visual workflow with multiple actions must include a condition or branch block")
	}
	indegree := make(map[string]int, len(nodes))
	adj := make(map[string][]string, len(nodes))
	for _, edge := range g.Edges {
		if _, ok := nodes[edge.From]; !ok {
			return fmt.Errorf("connection starts at unknown block %q", edge.From)
		}
		if _, ok := nodes[edge.To]; !ok || edge.From == edge.To {
			return fmt.Errorf("connection targets an invalid block")
		}
		if len(edge.Port) > 32 || strings.ContainsAny(edge.Port, "\r\n") {
			return errors.New("connection port is invalid")
		}
		if edge.Port != "" && edge.Port != "true" && edge.Port != "false" && edge.Port != "default" && edge.Port != "output" {
			return fmt.Errorf("unsupported connection port %q", edge.Port)
		}
		adj[edge.From] = append(adj[edge.From], edge.To)
		indegree[edge.To]++
	}
	queue := make([]string, 0, len(nodes))
	for id := range nodes {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	visited := make(map[string]bool, len(nodes))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited[id] = true
		for _, next := range adj[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(visited) != len(nodes) {
		return errors.New("visual workflow connections must not contain cycles")
	}
	triggerID := ""
	for _, node := range g.Nodes {
		if node.Kind == "trigger" {
			triggerID = node.ID
			break
		}
	}
	reachable := map[string]bool{triggerID: true}
	for changed := true; changed; {
		changed = false
		for from, nexts := range adj {
			if !reachable[from] {
				continue
			}
			for _, next := range nexts {
				if !reachable[next] {
					reachable[next] = true
					changed = true
				}
			}
		}
	}
	if len(reachable) != len(nodes) {
		return errors.New("every visual block must be reachable from the trigger")
	}
	return nil
}

func okBuiltinBlock(id string) bool {
	_, ok := builtinBlockKind(id)
	return ok
}

func builtinBlockKind(id string) (string, bool) {
	for _, block := range Blocks {
		if block.ID == id {
			if id == "action.file-output" {
				return "output", true
			}
			return block.Kind, true
		}
	}
	return "", false
}

// ApplyGraphToDefinition lets new clients submit only a graph while old
// clients continue to send the flat C.1 fields. When both are present the
// graph is authoritative for the fields it describes.
func ApplyGraphToDefinition(d *Definition) error {
	if d.Graph == nil {
		return nil
	}
	if err := ValidateGraph(*d.Graph); err != nil {
		return err
	}
	falseTargets := make(map[string]bool)
	for _, edge := range d.Graph.Edges {
		if edge.Port == "false" {
			falseTargets[edge.To] = true
		}
	}
	var trigger, action, fallbackAction, output *GraphNode
	var conditions []GraphNode
	for i := range d.Graph.Nodes {
		node := &d.Graph.Nodes[i]
		switch node.Kind {
		case "trigger":
			trigger = node
		case "condition":
			conditions = append(conditions, *node)
		case "action":
			if falseTargets[node.ID] || node.ID == "fallback" {
				fallbackAction = node
			} else if action == nil {
				action = node
			}
		case "output":
			output = node
		}
	}
	if trigger != nil {
		trigType := firstString(trigger.Config, "type", strings.TrimPrefix(trigger.BlockID, "trigger."))
		if trigType == "schedule" || trigType == "folder-scan" {
			trigType = "folder.scan"
		}
		d.Trigger.Type = trigType
		d.Trigger.InputDriveID = firstString(trigger.Config, "inputDriveId", d.Trigger.InputDriveID)
		d.Trigger.IntervalSeconds = firstInt(trigger.Config, "intervalSeconds", d.Trigger.IntervalSeconds)
		d.Trigger.StabilitySeconds = firstInt(trigger.Config, "stabilitySeconds", d.Trigger.StabilitySeconds)
		d.Trigger.MaxConcurrent = firstInt(trigger.Config, "maxConcurrent", d.Trigger.MaxConcurrent)
		d.Trigger.ProcessExistingOnNext = firstBool(trigger.Config, "processExistingOnNextScan", d.Trigger.ProcessExistingOnNext)
	}
	if len(conditions) > 0 {
		if raw, ok := conditions[0].Config["tree"]; ok {
			encoded, err := json.Marshal(raw)
			if err != nil || json.Unmarshal(encoded, &d.Condition) != nil {
				return errors.New("visual condition tree is invalid")
			}
		} else if conditions[0].BlockID == "condition.inspect-metadata" {
			d.Condition = conditionFromMetadataConfig(conditions[0].Config)
		} else {
			*d = applyFilterConfig(*d, conditions[0].Config)
		}
	}
	if action != nil {
		d.Action.Mode = firstString(action.Config, "mode", strings.TrimPrefix(action.BlockID, "action."))
		d.Action.ProfileID = firstString(action.Config, "profileId", d.Action.ProfileID)
		d.Action.ProfileVersion = firstString(action.Config, "profileVersion", d.Action.ProfileVersion)
		d.Action.TargetFormat = firstString(action.Config, "targetFormat", d.Action.TargetFormat)
		if opts, ok := action.Config["options"].(map[string]any); ok {
			d.Action.Options = make(map[string]string, len(opts))
			for key, value := range opts {
				if text, ok := value.(string); ok {
					d.Action.Options[key] = text
				}
			}
		}
	}
	if fallbackAction != nil {
		fb := FileAction{
			Mode:           firstString(fallbackAction.Config, "mode", strings.TrimPrefix(fallbackAction.BlockID, "action.")),
			ProfileID:      firstString(fallbackAction.Config, "profileId", ""),
			ProfileVersion: firstString(fallbackAction.Config, "profileVersion", ""),
			TargetFormat:   firstString(fallbackAction.Config, "targetFormat", ""),
		}
		if opts, ok := fallbackAction.Config["options"].(map[string]any); ok {
			fb.Options = make(map[string]string, len(opts))
			for key, value := range opts {
				if text, ok := value.(string); ok {
					fb.Options[key] = text
				}
			}
		}
		d.FallbackAction = &fb
	}
	if output != nil {
		d.Action.OutputDriveID = firstString(output.Config, "outputDriveId", d.Action.OutputDriveID)
		d.Action.OutputBehavior = firstString(output.Config, "behavior", d.Action.OutputBehavior)
	}
	return nil
}

func conditionFromMetadataConfig(config map[string]any) *Condition {
	var children []Condition
	if w := firstInt64(config, "minWidth", 0); w > 0 {
		children = append(children, Condition{Operator: "min_width", Bytes: w})
	}
	if h := firstInt64(config, "minHeight", 0); h > 0 {
		children = append(children, Condition{Operator: "min_height", Bytes: h})
	}
	if v, ok := config["hdr"]; ok {
		switch tv := v.(type) {
		case bool:
			if tv {
				children = append(children, Condition{Operator: "hdr", Value: "true"})
			} else {
				children = append(children, Condition{Operator: "hdr", Value: "false"})
			}
		case string:
			if tv == "true" || tv == "false" {
				children = append(children, Condition{Operator: "hdr", Value: tv})
			}
		}
	}
	if codec := firstString(config, "codec", ""); codec != "" {
		children = append(children, Condition{Operator: "codec", Value: codec})
	}
	if v, ok := config["hasAudio"]; ok {
		switch tv := v.(type) {
		case bool:
			if tv {
				children = append(children, Condition{Operator: "has_audio", Value: "true"})
			} else {
				children = append(children, Condition{Operator: "has_audio", Value: "false"})
			}
		case string:
			if tv == "true" || tv == "false" {
				children = append(children, Condition{Operator: "has_audio", Value: tv})
			}
		}
	}
	if len(children) == 0 {
		return nil
	}
	if len(children) == 1 {
		return &children[0]
	}
	return &Condition{Operator: "and", Children: children}
}

func applyFilterConfig(d Definition, config map[string]any) Definition {
	if values, ok := config["extensions"].([]any); ok {
		d.Filter.Extensions = d.Filter.Extensions[:0]
		for _, value := range values {
			if text, ok := value.(string); ok {
				d.Filter.Extensions = append(d.Filter.Extensions, strings.TrimPrefix(text, "."))
			}
		}
	} else if values, ok := config["extensions"].([]string); ok {
		d.Filter.Extensions = append([]string(nil), values...)
	}
	d.Filter.NameGlob = firstString(config, "nameGlob", d.Filter.NameGlob)
	d.Filter.MinBytes = firstInt64(config, "minBytes", d.Filter.MinBytes)
	d.Filter.MaxBytes = firstInt64(config, "maxBytes", d.Filter.MaxBytes)
	return d
}

func firstString(config map[string]any, key, fallback string) string {
	if value, ok := config[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func firstInt(config map[string]any, key string, fallback int) int {
	return int(firstInt64(config, key, int64(fallback)))
}

func firstInt64(config map[string]any, key string, fallback int64) int64 {
	switch value := config[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	}
	return fallback
}

func firstBool(config map[string]any, key string, fallback bool) bool {
	if value, ok := config[key].(bool); ok {
		return value
	}
	return fallback
}
