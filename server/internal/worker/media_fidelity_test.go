package worker

import (
	"encoding/json"
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

func TestValidateMediaFidelityPreservesSelectedStreamsAndMetadata(t *testing.T) {
	video := hdrStream()
	video.Index = 0
	audio := mediaStream{Index: 1, CodecType: "audio", Channels: 2, ChannelLayout: "stereo", Tags: map[string]string{"language": "eng"}}
	source := mediaProbe{FormatTags: map[string]string{"title": "sample"}, Streams: []mediaStream{video, audio}}
	target := mediaProbe{FormatTags: map[string]string{"title": "sample"}, Streams: []mediaStream{video, audio}}
	if err := validateMediaFidelity(source, target, Invocation{StreamSelection: "all", PreserveMetadata: true, PreserveHDR: true, DynamicHDRPolicy: "verify"}); err != nil {
		t.Fatal(err)
	}
	target.Streams = target.Streams[:1]
	if err := validateMediaFidelity(source, target, Invocation{StreamSelection: "all"}); !strings.Contains(err.Error(), "all source streams") {
		t.Fatalf("stream loss error=%v", err)
	}
	target.Streams = []mediaStream{video, audio}
	target.FormatTags["title"] = "changed"
	if err := validateMediaFidelity(source, target, Invocation{StreamSelection: "all", PreserveMetadata: true}); !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("metadata loss error=%v", err)
	}
}

func TestValidateMediaFidelityVerifiesDynamicHDRPayload(t *testing.T) {
	value := json.RawMessage(`"sample"`)
	sourceStream := hdrStream()
	sourceStream.SideData = append(sourceStream.SideData, mediaSideData{Type: "HDR10+ Dynamic Metadata", Values: map[string]json.RawMessage{"payload": value}})
	targetStream := hdrStream()
	targetStream.SideData = append(targetStream.SideData, mediaSideData{Type: "HDR10+ Dynamic Metadata", Values: map[string]json.RawMessage{"payload": value}})
	if err := validateMediaFidelity(mediaProbe{Streams: []mediaStream{sourceStream}}, mediaProbe{Streams: []mediaStream{targetStream}}, Invocation{PreserveHDR: true, HDRPolicy: "preserve", DynamicHDRPolicy: "verify"}); err != nil {
		t.Fatal(err)
	}
	targetStream.SideData[len(targetStream.SideData)-1].Values["payload"] = json.RawMessage(`"changed"`)
	if err := validateMediaFidelity(mediaProbe{Streams: []mediaStream{sourceStream}}, mediaProbe{Streams: []mediaStream{targetStream}}, Invocation{PreserveHDR: true, HDRPolicy: "preserve", DynamicHDRPolicy: "verify"}); !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("dynamic payload loss error=%v", err)
	}
}

func TestValidateMediaFidelityChecksEveryVideoInAllStreamPolicy(t *testing.T) {
	first := hdrStream()
	first.Index = 0
	second := hdrStream()
	second.Index = 2
	second.Width = 1920
	second.Height = 1080
	source := mediaProbe{Streams: []mediaStream{first, {Index: 1, CodecType: "audio"}, second}}
	target := mediaProbe{Streams: []mediaStream{first, {Index: 1, CodecType: "audio"}, second}}
	if err := validateMediaFidelity(source, target, Invocation{StreamSelection: "all", PreserveResolution: true, HDRPolicy: "preserve"}); err != nil {
		t.Fatal(err)
	}
	target.Streams[2].ColorTransfer = "bt709"
	if err := validateMediaFidelity(source, target, Invocation{StreamSelection: "all", PreserveHDR: true}); !strings.Contains(err.Error(), "color transfer") {
		t.Fatalf("second video downgrade error=%v", err)
	}
}

func TestValidateMediaFidelityAllowsExplicitAudioOnlySelection(t *testing.T) {
	source := mediaProbe{Streams: []mediaStream{hdrStream(), {Index: 1, CodecType: "audio"}}}
	target := mediaProbe{Streams: []mediaStream{{Index: 0, CodecType: "audio"}}}
	if err := validateMediaFidelity(source, target, Invocation{StreamSelection: "audio"}); err != nil {
		t.Fatal(err)
	}
}
