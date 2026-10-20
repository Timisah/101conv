package worker

import (
	"strings"
	"testing"
)

func hdrStream() mediaStream {
	return mediaStream{
		CodecType:        "video",
		Width:            3840,
		Height:           2160,
		ColorPrimaries:   "bt2020",
		ColorTransfer:    "smpte2084",
		ColorSpace:       "bt2020nc",
		ColorRange:       "tv",
		BitsPerRawSample: "10",
		SideData:         []mediaSideData{{Type: "Mastering display metadata"}, {Type: "Content light level metadata"}},
	}
}

func TestValidateMediaFidelityPreservesResolutionAndHDR(t *testing.T) {
	source := mediaProbe{Streams: []mediaStream{hdrStream()}}
	target := mediaProbe{Streams: []mediaStream{hdrStream()}}
	if err := validateMediaFidelity(source, target, Invocation{PreserveResolution: true, PreserveHDR: true}); err != nil {
		t.Fatal(err)
	}

	target.Streams[0].Width = 1920
	if err := validateMediaFidelity(source, target, Invocation{PreserveResolution: true, PreserveHDR: true}); !strings.Contains(err.Error(), "resolution") {
		t.Fatalf("resolution downgrade error=%v", err)
	}
	target.Streams[0] = hdrStream()
	target.Streams[0].ColorTransfer = "bt709"
	if err := validateMediaFidelity(source, target, Invocation{PreserveHDR: true}); !strings.Contains(err.Error(), "color transfer") {
		t.Fatalf("transfer downgrade error=%v", err)
	}
	target.Streams[0] = hdrStream()
	target.Streams[0].BitsPerRawSample = "8"
	if err := validateMediaFidelity(source, target, Invocation{PreserveHDR: true}); !strings.Contains(err.Error(), "bit depth") {
		t.Fatalf("bit-depth downgrade error=%v", err)
	}
	target.Streams[0] = hdrStream()
	target.Streams[0].SideData = []mediaSideData{{Type: "Mastering display metadata"}}
	if err := validateMediaFidelity(source, target, Invocation{PreserveHDR: true}); !strings.Contains(err.Error(), "content light") {
		t.Fatalf("static metadata downgrade error=%v", err)
	}
}

func TestValidateMediaFidelityRejectsMissingDynamicHDR(t *testing.T) {
	source := mediaProbe{Streams: []mediaStream{hdrStream()}}
	source.Streams[0].SideData = append(source.Streams[0].SideData, mediaSideData{Type: "HDR10+ Dynamic Metadata"})
	target := mediaProbe{Streams: []mediaStream{hdrStream()}}
	if err := validateMediaFidelity(source, target, Invocation{PreserveHDR: true}); !strings.Contains(err.Error(), "dynamic HDR") {
		t.Fatalf("dynamic HDR downgrade error=%v", err)
	}
}
