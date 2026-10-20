package acceleration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigFromEnvRequiresExplicitDeviceAllowlist(t *testing.T) {
	config, err := ConfigFromEnv(func(key string) string {
		if key == "GPU_RENDER_NODES" {
			return ""
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.HardwareConfigured() {
		t.Fatal("blank device configuration enabled hardware")
	}
	if _, err = ConfigFromEnv(func(key string) string {
		if key == "GPU_RENDER_NODES" {
			return "/dev/dri/renderD128,/tmp/not-a-render-node"
		}
		return ""
	}); err == nil {
		t.Fatal("accepted a device outside the render-node allowlist")
	}
}

func TestDetectIntelAndFallback(t *testing.T) {
	device := "/dev/dri/renderD128"
	probe := SystemProbe()
	probe.Stat = func(path string) (os.FileInfo, error) {
		if path == device {
			return fakeFileInfo{}, nil
		}
		return nil, errors.New("unexpected stat")
	}
	probe.ReadFile = func(path string) ([]byte, error) {
		if filepath.Base(filepath.Dir(filepath.Dir(path))) == "renderD128" {
			return []byte("0x8086\n"), nil
		}
		return nil, errors.New("unexpected read")
	}
	probe.Readlink = func(string) (string, error) { return "/sys/bus/pci/drivers/i915", nil }
	report, err := Detect(context.Background(), Config{RenderNodes: []string{device}, SysfsRoot: "/sys"}, probe)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	var qsvFound bool
	for _, item := range report.Accelerators {
		if item.ID == "intel-vaapi/renderD128" {
			found = true
			if !item.Available || item.Driver != "i915" || !item.Encode || !item.Decode {
				t.Fatalf("unexpected Intel capability: %+v", item)
			}
		}
		if item.ID == "intel-qsv/renderD128" && item.API == "qsv" {
			qsvFound = true
		}
	}
	if !found || !qsvFound || !report.CPUFallback || !report.HardwareConfigured {
		t.Fatalf("incomplete report: %+v", report)
	}
	selection, err := report.Resolve(Request{Encode: true, Decode: true, PreserveHDR: true})
	if err != nil || selection.Accelerator.Vendor != VendorCPU {
		t.Fatalf("HDR selection = %+v, %v", selection, err)
	}
}

func TestResolveCanRequireHardware(t *testing.T) {
	report := Report{SchemaVersion: 1, CPUFallback: true, Accelerators: []Accelerator{{ID: "cpu", Vendor: VendorCPU, Available: true, Encode: true, Decode: true, HDRPreserving: true}}}
	if _, err := report.Resolve(Request{Encode: true, RequireHardware: true}); err == nil {
		t.Fatal("required hardware unexpectedly used CPU fallback")
	}
}

func TestDetectNVIDIARequiresDriverProbe(t *testing.T) {
	probe := SystemProbe()
	probe.Stat = func(path string) (os.FileInfo, error) {
		if filepath.Base(path) == "nvidia0" || filepath.Base(path) == "nvidiactl" {
			return fakeFileInfo{}, nil
		}
		return nil, errors.New("missing")
	}
	probe.LookPath = func(string) (string, error) { return "/usr/bin/nvidia-smi", nil }
	probe.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("driver failed") }
	report, err := Detect(context.Background(), Config{NVIDIADevices: []string{"/dev/nvidia0"}, SysfsRoot: "/sys"}, probe)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range report.Accelerators {
		if item.Vendor == VendorNVIDIA && item.Available {
			t.Fatalf("reported unavailable NVIDIA driver as available: %+v", item)
		}
	}
}

func TestDetectAMDAndNVIDIAWhenConfigured(t *testing.T) {
	probe := SystemProbe()
	probe.Stat = func(path string) (os.FileInfo, error) {
		if path == "/dev/dri/renderD129" || path == "/dev/nvidia0" || path == "/dev/nvidiactl" {
			return fakeFileInfo{}, nil
		}
		return nil, errors.New("missing")
	}
	probe.ReadFile = func(string) ([]byte, error) { return []byte("0x1002"), nil }
	probe.Readlink = func(string) (string, error) { return "/sys/bus/pci/drivers/amdgpu", nil }
	probe.LookPath = func(string) (string, error) { return "/usr/bin/nvidia-smi", nil }
	probe.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("GPU\n"), nil }
	report, err := Detect(context.Background(), Config{RenderNodes: []string{"/dev/dri/renderD129"}, NVIDIADevices: []string{"/dev/nvidia0"}, SysfsRoot: "/sys"}, probe)
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{}
	for _, item := range report.Accelerators {
		if item.Available {
			available[item.ID] = true
		}
	}
	if !available["amd-vaapi/renderD129"] || !available["nvidia-nvenc/nvidia0"] {
		t.Fatalf("configured AMD/NVIDIA devices were not detected: %+v", report)
	}
}

type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "device" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }
