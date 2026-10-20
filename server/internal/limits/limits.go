// Package limits contains shared resource ceilings enforced across the API,
// persistence, and Worker boundaries.
package limits

const (
	// MaxInputBytes is a safety ceiling, not the default guest quota. Uploads
	// are streamed from persistent storage and the Worker applies a separate
	// output ceiling, so installations can safely choose values such as 150 MiB
	// without making the UI/API cap arbitrary at 100 MiB.
	MaxInputBytes  int64 = 1 << 30
	MaxOutputBytes int64 = 200 << 20
)
