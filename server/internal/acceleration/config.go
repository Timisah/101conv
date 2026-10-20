package acceleration

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	renderNodeName   = regexp.MustCompile(`^renderD[0-9]+$`)
	nvidiaDeviceName = regexp.MustCompile(`^nvidia[0-9]+$`)
)

// ConfigFromEnv reads the explicit Worker device allowlists. A blank value is
// deliberate CPU-only mode. Commas are accepted in addition to the platform
// path-list separator so Compose values are easy to read.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	config := Config{SysfsRoot: "/sys"}
	if value := strings.TrimSpace(getenv("GPU_SYSFS_ROOT")); value != "" {
		config.SysfsRoot = filepath.Clean(value)
	}
	var err error
	if config.RenderNodes, err = parseAllowlist(getenv("GPU_RENDER_NODES"), "/dev/dri/renderD"); err != nil {
		return Config{}, fmt.Errorf("GPU_RENDER_NODES: %w", err)
	}
	if config.NVIDIADevices, err = parseAllowlist(getenv("GPU_NVIDIA_DEVICES"), "/dev/nvidia"); err != nil {
		return Config{}, fmt.Errorf("GPU_NVIDIA_DEVICES: %w", err)
	}
	return config, config.Validate()
}

func parseAllowlist(value, prefix string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == os.PathListSeparator })
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		path := filepath.Clean(strings.TrimSpace(part))
		if path == "." || !filepath.IsAbs(path) || !strings.HasPrefix(path, prefix) || path == prefix || strings.ContainsRune(path, '\x00') {
			return nil, fmt.Errorf("path %q is not an allowed device path", part)
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out, nil
}

func validDevicePath(path, parent string, namePattern *regexp.Regexp) bool {
	return filepath.IsAbs(path) && filepath.Dir(path) == parent && filepath.Clean(path) == path && !strings.ContainsRune(path, '\x00') && namePattern.MatchString(filepath.Base(path))
}
