// Package automation contains the persisted, declarative automation model.
// Blocks describe data transformations; they never contain executable code.
package automation

import "time"

const ExtensionAPIVersion = "1.0.0"

type Drive struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Path            string    `json:"path"`
	Access          string    `json:"access"` // read or read_write
	Enabled         bool      `json:"enabled"`
	Accessible      bool      `json:"accessible"`
	Writable        bool      `json:"writable"`
	FreeBytes       uint64    `json:"freeBytes,omitempty"`
	HealthError     string    `json:"healthError,omitempty"`
	AutomationCount int       `json:"automationCount"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type Definition struct {
	Trigger   Trigger    `json:"trigger"`
	Filter    FileFilter `json:"filter"`
	Condition *Condition `json:"condition,omitempty"`
	Action    FileAction `json:"action"`
}

type Trigger struct {
	Type                  string `json:"type"` // folder.scan or manual
	InputDriveID          string `json:"inputDriveId"`
	IntervalSeconds       int    `json:"intervalSeconds,omitempty"`
	StabilitySeconds      int    `json:"stabilitySeconds,omitempty"`
	MaxConcurrent         int    `json:"maxConcurrent,omitempty"`
	ProcessExistingOnNext bool   `json:"processExistingOnNextScan"`
}

// Condition is a small declarative expression tree; it cannot invoke code.
// Operators are and, or, not, extension, name_glob, min_size, and max_size.
type Condition struct {
	Operator string      `json:"operator"`
	Value    string      `json:"value,omitempty"`
	Bytes    int64       `json:"bytes,omitempty"`
	Children []Condition `json:"children,omitempty"`
}

type FileFilter struct {
	Extensions []string `json:"extensions,omitempty"`
	NameGlob   string   `json:"nameGlob,omitempty"`
	MinBytes   int64    `json:"minBytes,omitempty"`
	MaxBytes   int64    `json:"maxBytes,omitempty"`
}

type FileAction struct {
	Mode           string `json:"mode"` // convert or compress
	ProfileID      string `json:"profileId"`
	ProfileVersion string `json:"profileVersion,omitempty"`
	TargetFormat   string `json:"targetFormat"`
	OutputDriveID  string `json:"outputDriveId"`
	OutputBehavior string `json:"outputBehavior"` // create, skip_existing, unique
}

type Automation struct {
	ID            string     `json:"id"`
	OwnerID       string     `json:"ownerId"`
	Name          string     `json:"name"`
	Enabled       bool       `json:"enabled"`
	Status        string     `json:"status"`
	Diagnostic    string     `json:"diagnostic,omitempty"`
	Revision      int        `json:"revision"`
	Definition    Definition `json:"definition"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	LastExecution *time.Time `json:"lastExecution,omitempty"`
	LastScan      *time.Time `json:"lastScan,omitempty"`
}

type Run struct {
	ID           string     `json:"id"`
	AutomationID string     `json:"automationId"`
	Revision     int        `json:"revision"`
	FileName     string     `json:"fileName,omitempty"`
	State        string     `json:"state"`
	JobID        string     `json:"jobId,omitempty"`
	OutputName   string     `json:"outputName,omitempty"`
	Error        string     `json:"error,omitempty"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
}

type Block struct {
	ID                 string         `json:"id"`
	Version            string         `json:"version"`
	Kind               string         `json:"kind"`
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	ConfigSchema       map[string]any `json:"configSchema"`
	RequiredPermission string         `json:"requiredPermission"`
}

type Blueprint struct {
	ID          string     `json:"id"`
	Version     string     `json:"version"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Mode        string     `json:"mode"`
	Definition  Definition `json:"definition"`
}

// ExtensionManifest is a declarative compatibility contract. C.1 does not load
// or execute extension packages; package activation belongs to a later phase.
type ExtensionManifest struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Publisher           string            `json:"publisher"`
	Version             string            `json:"version"`
	ExtensionAPI        string            `json:"extensionApi"`
	ExtensionAPIRange   APIVersionRange   `json:"extensionApiRange,omitempty"`
	ExternalApplication string            `json:"externalApplication,omitempty"`
	ExternalVersion     string            `json:"externalVersion,omitempty"`
	Permissions         []string          `json:"permissions,omitempty"`
	Capabilities        []string          `json:"capabilities,omitempty"`
	Blocks              []Block           `json:"blocks,omitempty"`
	Dependencies        map[string]string `json:"dependencies,omitempty"`
	Integrity           string            `json:"integrity,omitempty"`
	ConfigurationSchema int               `json:"configurationSchema"`
}

type APIVersionRange struct {
	Minimum          string `json:"minimum,omitempty"`
	MaximumExclusive string `json:"maximumExclusive,omitempty"`
}
