// Package acceleration discovers explicitly configured media devices for a
// Worker. Discovery is diagnostic and policy-aware: an empty allowlist means
// CPU-only execution, and no device is ever inferred from the host.
package acceleration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	VendorIntel  = "intel"
	VendorAMD    = "amd"
	VendorNVIDIA = "nvidia"
	VendorCPU    = "cpu"
)

// Config is the only device-access input accepted by the probe. Paths must be
// explicitly supplied by the deployment; the probe never scans /dev.
type Config struct {
	RenderNodes   []string
	NVIDIADevices []string
	SysfsRoot     string
}

func (c Config) HardwareConfigured() bool {
	return len(c.RenderNodes) > 0 || len(c.NVIDIADevices) > 0
}

// Accelerator is a public diagnostic record. Device is a basename rather
// than a host path so the Operations API does not disclose arbitrary mounts.
type Accelerator struct {
	ID            string `json:"id"`
	Vendor        string `json:"vendor"`
	API           string `json:"api"`
	Device        string `json:"device,omitempty"`
	Driver        string `json:"driver,omitempty"`
	Available     bool   `json:"available"`
	Encode        bool   `json:"encode"`
	Decode        bool   `json:"decode"`
	HDRPreserving bool   `json:"hdrPreserving"`
	Verification  string `json:"verification"`
	Reason        string `json:"reason"`
}

// Report is persisted in the Worker heartbeat and is safe to expose to an
// administrator. CPU is always included as the deterministic fallback.
type Report struct {
	SchemaVersion      int           `json:"schemaVersion"`
	HardwareConfigured bool          `json:"hardwareConfigured"`
	CPUFallback        bool          `json:"cpuFallback"`
	Accelerators       []Accelerator `json:"accelerators"`
}

type Request struct {
	Encode          bool
	Decode          bool
	PreserveHDR     bool
	RequireHardware bool
}

type Selection struct {
	Accelerator Accelerator `json:"accelerator"`
	Strategy    string      `json:"strategy"`
}

// Probe abstracts host inspection so discovery remains unit-testable without
// requiring a GPU in CI.
type Probe struct {
	Stat     func(string) (os.FileInfo, error)
	ReadFile func(string) ([]byte, error)
	Readlink func(string) (string, error)
	LookPath func(string) (string, error)
	Run      func(context.Context, string, ...string) ([]byte, error)
}

func SystemProbe() Probe {
	return Probe{
		Stat:     os.Stat,
		ReadFile: os.ReadFile,
		Readlink: os.Readlink,
		LookPath: exec.LookPath,
		Run: func(ctx context.Context, path string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, path, args...).Output()
		},
	}
}

func (c Config) Validate() error {
	if c.SysfsRoot == "" || !filepath.IsAbs(c.SysfsRoot) || filepath.Clean(c.SysfsRoot) != c.SysfsRoot {
		return fmt.Errorf("sysfs root must be a clean absolute path")
	}
	for _, node := range c.RenderNodes {
		if !validDevicePath(node, "/dev/dri", renderNodeName) {
			return fmt.Errorf("configured render node %q is not an allowed render node", node)
		}
	}
	for _, node := range c.NVIDIADevices {
		if !validDevicePath(node, "/dev", nvidiaDeviceName) {
			return fmt.Errorf("configured NVIDIA device %q is not an allowed GPU device", node)
		}
	}
	return nil
}

func Detect(ctx context.Context, config Config, probe Probe) (Report, error) {
	if err := config.Validate(); err != nil {
		return Report{}, err
	}
	if probe.Stat == nil || probe.ReadFile == nil || probe.Readlink == nil || probe.LookPath == nil || probe.Run == nil {
		return Report{}, fmt.Errorf("accelerator probe is incomplete")
	}
	report := Report{SchemaVersion: 1, HardwareConfigured: config.HardwareConfigured(), CPUFallback: true, Accelerators: []Accelerator{
		{ID: "cpu", Vendor: VendorCPU, API: "software", Available: true, Encode: true, Decode: true, HDRPreserving: true, Verification: "software", Reason: "deterministic CPU fallback"},
	}}
	sysfsRoot := config.SysfsRoot
	for _, node := range config.RenderNodes {
		item := inspectRenderNode(node, sysfsRoot, probe)
		report.Accelerators = append(report.Accelerators, item)
		if item.Vendor == VendorIntel && item.Available {
			qsv := item
			qsv.ID = "intel-qsv/" + qsv.Device
			qsv.API = "qsv"
			qsv.Reason = "Intel Quick Sync device and allowlisted driver detected; codec support remains engine-specific"
			report.Accelerators = append(report.Accelerators, qsv)
		}
	}
	for _, node := range config.NVIDIADevices {
		report.Accelerators = append(report.Accelerators, inspectNVIDIA(ctx, node, probe))
	}
	sort.SliceStable(report.Accelerators, func(i, j int) bool {
		if report.Accelerators[i].Vendor == report.Accelerators[j].Vendor {
			return report.Accelerators[i].ID < report.Accelerators[j].ID
		}
		return report.Accelerators[i].Vendor < report.Accelerators[j].Vendor
	})
	return report, nil
}

func inspectRenderNode(node, sysfsRoot string, probe Probe) Accelerator {
	device := filepath.Base(node)
	base := Accelerator{ID: "render-node/" + device, Device: device, Verification: "device-and-driver", Reason: "configured render node is unavailable"}
	if _, err := probe.Stat(node); err != nil {
		return base
	}
	vendorBytes, err := probe.ReadFile(filepath.Join(sysfsRoot, "class", "drm", device, "device", "vendor"))
	if err != nil {
		base.Reason = "GPU vendor could not be verified from sysfs"
		return base
	}
	driverTarget, err := probe.Readlink(filepath.Join(sysfsRoot, "class", "drm", device, "device", "driver"))
	if err != nil {
		base.Reason = "GPU driver could not be verified from sysfs"
		return base
	}
	driver := filepath.Base(filepath.Clean(driverTarget))
	vendor := strings.ToLower(strings.TrimSpace(string(vendorBytes)))
	switch vendor {
	case "0x8086":
		base.Vendor, base.Driver = VendorIntel, driver
		if driver != "i915" && driver != "xe" {
			base.Reason = "Intel render node uses an unsupported driver"
			return base
		}
		base.ID, base.API, base.Available, base.Encode, base.Decode = "intel-vaapi/"+device, "vaapi", true, true, true
		base.Reason = "Intel VAAPI device and allowlisted driver detected; codec support remains engine-specific"
		return base
	case "0x1002":
		base.Vendor, base.Driver = VendorAMD, driver
		if driver != "amdgpu" {
			base.Reason = "AMD render node uses an unsupported driver"
			return base
		}
		base.ID, base.API, base.Available, base.Encode, base.Decode = "amd-vaapi/"+device, "vaapi", true, true, true
		base.Reason = "AMD VAAPI device and allowlisted driver detected; codec support remains engine-specific"
		return base
	default:
		base.Vendor = "unknown"
		base.Driver = driver
		base.Reason = "render node vendor is not in the supported acceleration allowlist"
		return base
	}
}

func inspectNVIDIA(ctx context.Context, node string, probe Probe) Accelerator {
	device := filepath.Base(node)
	base := Accelerator{ID: "nvidia/" + device, Vendor: VendorNVIDIA, API: "nvenc-nvdec", Device: device, Verification: "device-and-driver", Reason: "configured NVIDIA device is unavailable"}
	if _, err := probe.Stat(node); err != nil {
		return base
	}
	control := filepath.Join(filepath.Dir(node), "nvidiactl")
	if _, err := probe.Stat(control); err != nil {
		base.Reason = "NVIDIA control device is unavailable"
		return base
	}
	smi, err := probe.LookPath("nvidia-smi")
	if err != nil {
		base.Reason = "nvidia-smi is unavailable; NVIDIA driver could not be verified"
		return base
	}
	if _, err = probe.Run(ctx, smi, "--query-gpu=name", "--format=csv,noheader"); err != nil {
		base.Reason = "NVIDIA driver probe failed"
		return base
	}
	base.ID, base.Available, base.Encode, base.Decode = "nvidia-nvenc/"+device, true, true, true
	base.Reason = "NVIDIA device and driver probe succeeded; codec support remains engine-specific"
	return base
}

// Resolve chooses hardware only when its verified properties satisfy the
// request. A non-HDR request may use the first stable hardware record; an HDR
// preserving request intentionally falls back to CPU until the engine proves
// that the selected codec path preserves the source metadata.
func (r Report) Resolve(request Request) (Selection, error) {
	items := append([]Accelerator(nil), r.Accelerators...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	for _, item := range items {
		if item.Vendor == VendorCPU || !item.Available || (request.Encode && !item.Encode) || (request.Decode && !item.Decode) || request.PreserveHDR && !item.HDRPreserving {
			continue
		}
		return Selection{Accelerator: item, Strategy: "verified hardware capability"}, nil
	}
	if !request.RequireHardware && r.CPUFallback {
		for _, item := range items {
			if item.Vendor == VendorCPU && item.Available {
				strategy := "CPU fallback; no compatible hardware capability"
				if request.PreserveHDR {
					strategy = "CPU fallback; HDR preservation is not verified for hardware encoders"
				}
				return Selection{Accelerator: item, Strategy: strategy}, nil
			}
		}
	}
	return Selection{}, fmt.Errorf("no acceleration capability satisfies the requested encode/decode and fidelity constraints")
}
