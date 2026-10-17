package jobs

import "time"

type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

type Upload struct {
	ID        string    `json:"id"`
	FileName  string    `json:"fileName"`
	Extension string    `json:"extension"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"createdAt"`
}

type Job struct {
	ID           string    `json:"id"`
	ProfileID    string    `json:"profileId"`
	ProfileVer   string    `json:"profileVersion"`
	SourceFormat string    `json:"sourceFormat"`
	TargetFormat string    `json:"targetFormat"`
	Mode         string    `json:"mode"`
	State        State     `json:"state"`
	Progress     int       `json:"progress"`
	Attempt      int       `json:"attempt"`
	MaxAttempts  int       `json:"maxAttempts"`
	Error        string    `json:"error,omitempty"`
	OutputSize   int64     `json:"outputSize,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Attempt struct {
	Number    int       `json:"number"`
	State     string    `json:"state"`
	StartedAt time.Time `json:"startedAt"`
	Finished  time.Time `json:"finishedAt,omitempty"`
	ExitCode  *int      `json:"exitCode,omitempty"`
	Stdout    string    `json:"stdout,omitempty"`
	Stderr    string    `json:"stderr,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type SubmitRequest struct {
	UploadID       string            `json:"uploadId"`
	ProfileID      string            `json:"profileId"`
	TargetFormat   string            `json:"targetFormat"`
	Mode           string            `json:"mode,omitempty"`
	Options        map[string]string `json:"options,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey"`
}

// ProfilePreference is an exact format mapping. ProfileID may be retained even
// while the referenced profile is temporarily unavailable; resolution never
// executes it until the management service confirms compatibility.
type ProfilePreference struct {
	Mode         string `json:"mode"`
	SourceFormat string `json:"sourceFormat"`
	TargetFormat string `json:"targetFormat"`
	ProfileID    string `json:"profileId"`
}

type ProfileChoice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type PreferenceMapping struct {
	Mode             string          `json:"mode"`
	SourceFormat     string          `json:"sourceFormat"`
	TargetFormat     string          `json:"targetFormat"`
	GlobalProfileID  string          `json:"globalProfileId,omitempty"`
	UserProfileID    string          `json:"userProfileId,omitempty"`
	EffectiveProfile string          `json:"effectiveProfileId,omitempty"`
	EffectiveSource  string          `json:"effectiveSource"`
	GlobalAvailable  bool            `json:"globalAvailable"`
	UserAvailable    bool            `json:"userAvailable"`
	Alternatives     []ProfileChoice `json:"alternatives"`
}

type ExecutionSpec struct {
	EngineID                    string   `json:"engineId"`
	EngineVersion               string   `json:"engineVersion"`
	ExecutableName              string   `json:"executableName"`
	ExecutableRelativePath      string   `json:"executableRelativePath"`
	Arguments                   []string `json:"arguments"`
	SourceExtension             string   `json:"sourceExtension"`
	TargetExtension             string   `json:"targetExtension"`
	CompressionArguments        []string `json:"compressionArguments,omitempty"`
	TargetSizeBytes             int64    `json:"targetSizeBytes,omitempty"`
	ProbeExecutableRelativePath string   `json:"probeExecutableRelativePath,omitempty"`
	AudioBitrateKbps            int      `json:"audioBitrateKbps,omitempty"`
	MinimumVideoKbps            int      `json:"minimumVideoKbps,omitempty"`
	RequireSmaller              bool     `json:"requireSmallerOutput,omitempty"`
}

type JobDetail struct {
	Job      Job       `json:"job"`
	Attempts []Attempt `json:"attempts"`
}
