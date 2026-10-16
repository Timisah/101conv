// Package worker defines the invocation boundary between profiles and process
// execution. Production runners can implement this with a restricted process
// or isolated container without changing profile or repository models.
package worker

import (
	"context"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/profile"
)

type Invocation struct {
	ProfileID      string                 `json:"profileId"`
	ProfileVersion string                 `json:"profileVersion"`
	Rule           profile.ConversionRule `json:"rule"`
	Engine         engine.Installation    `json:"engine"`
	SourcePath     string                 `json:"sourcePath"`
	TargetPath     string                 `json:"targetPath"`
	Options        map[string]string      `json:"options,omitempty"`
}

type Result struct {
	OutputPath string `json:"outputPath"`
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
}

// Runner implementations must apply filesystem, network, CPU, memory, process,
// duration, and output limits. Stdout and stderr in Result must remain bounded.
type Runner interface {
	Run(context.Context, Invocation) (Result, error)
}
