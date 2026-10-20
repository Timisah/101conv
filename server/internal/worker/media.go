package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/timisah/101conv/server/internal/media"
)

type mediaProbe = media.Probe
type mediaStream = media.Stream
type mediaSideData = media.SideData

func probeMedia(parent context.Context, engineRoot, work, inputPath string, in Invocation) (*mediaProbe, error) {
	executable := filepath.Join(engineRoot, in.EngineID, in.EngineVersion, filepath.FromSlash(in.ProbeExecutableRelativePath))
	if err := rejectSymlinkPath(engineRoot, executable); err != nil {
		return nil, fmt.Errorf("resolve media probe: %w", err)
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("compression probe executable is unavailable")
	}
	return media.ProbeFile(parent, executable, work, inputPath)
}

func validateMediaFidelity(input, output mediaProbe, in Invocation) error {
	if err := validateStreamSelection(input, output, in.StreamSelection); err != nil {
		return err
	}
	sourceVideos := videoStreams(input)
	if len(sourceVideos) == 0 || in.StreamSelection == "audio" {
		if in.PreserveMetadata {
			return validateMetadata(input, output)
		}
		return nil
	}
	targetVideos := videoStreams(output)
	if len(targetVideos) == 0 {
		return fmt.Errorf("output lost the source video stream")
	}
	if in.StreamSelection == "all" {
		if len(sourceVideos) != len(targetVideos) {
			return fmt.Errorf("output did not preserve all source video streams")
		}
		for i := range sourceVideos {
			if err := validateVideoFidelity(sourceVideos[i], targetVideos[i], in); err != nil {
				return err
			}
		}
	} else if err := validateVideoFidelity(sourceVideos[0], targetVideos[0], in); err != nil {
		return err
	}
	if in.PreserveMetadata {
		return validateMetadata(input, output)
	}
	return nil
}

func videoStreams(probe mediaProbe) []mediaStream {
	streams := []mediaStream{}
	for _, stream := range probe.Streams {
		if strings.EqualFold(stream.CodecType, "video") {
			streams = append(streams, stream)
		}
	}
	return streams
}

func validateVideoFidelity(source, target mediaStream, in Invocation) error {
	if in.PreserveResolution && (source.Width < 1 || source.Height < 1 || target.Width != source.Width || target.Height != source.Height) {
		return fmt.Errorf("output changed the source video resolution")
	}
	hdrPolicy := in.HDRPolicy
	if hdrPolicy == "" && in.PreserveHDR {
		hdrPolicy = "preserve"
	}
	sourceHDR, sourceDynamic := source.HDR()
	targetHDR, targetDynamic := target.HDR()
	if sourceHDR && hdrPolicy != "" {
		switch hdrPolicy {
		case "allow-sdr":
			// An explicit policy may permit an SDR result; it is never the
			// implicit behavior of a preserve profile.
		case "tone-map":
			if targetHDR {
				return fmt.Errorf("tone-map policy produced HDR output")
			}
		case "", "preserve":
			if !targetHDR {
				return fmt.Errorf("output lost source HDR signaling")
			}
			properties := []struct {
				label       string
				sourceValue string
				targetValue string
			}{
				{label: "color primaries", sourceValue: source.ColorPrimaries, targetValue: target.ColorPrimaries},
				{label: "color transfer", sourceValue: source.ColorTransfer, targetValue: target.ColorTransfer},
				{label: "color matrix", sourceValue: source.ColorSpace, targetValue: target.ColorSpace},
				{label: "color range", sourceValue: source.ColorRange, targetValue: target.ColorRange},
			}
			for _, property := range properties {
				if property.sourceValue != "" && !strings.EqualFold(property.sourceValue, property.targetValue) {
					return fmt.Errorf("output changed source %s", property.label)
				}
			}
			if sourceDepth := source.BitDepth(); sourceDepth > 0 && target.BitDepth() < sourceDepth {
				return fmt.Errorf("output reduced source video bit depth")
			}
			for _, label := range []string{"mastering display metadata", "content light level metadata"} {
				if source.SideDataContains(label) && !target.SideDataContains(label) {
					return fmt.Errorf("output lost source %s", label)
				}
			}
			if sourceDynamic {
				switch in.DynamicHDRPolicy {
				case "reject":
					return fmt.Errorf("source dynamic HDR is unsupported by this profile")
				case "verify":
					if !targetDynamic || !sameDynamicSideData(source, target) {
						return fmt.Errorf("output did not preserve dynamic HDR metadata exactly")
					}
				default:
					if !targetDynamic {
						return fmt.Errorf("output lost source dynamic HDR metadata")
					}
				}
			}
		}
	} else if sourceDynamic && in.DynamicHDRPolicy == "verify" && (!targetDynamic || !sameDynamicSideData(source, target)) {
		return fmt.Errorf("output did not preserve dynamic HDR metadata exactly")
	}
	return nil
}

func validateMediaPolicy(in Invocation) error {
	switch in.StreamSelection {
	case "", "all", "first-video-audio", "first-video", "audio":
	default:
		return fmt.Errorf("unsupported stream selection policy")
	}
	switch in.HDRPolicy {
	case "", "preserve", "allow-sdr", "tone-map":
	default:
		return fmt.Errorf("unsupported HDR policy")
	}
	switch in.DynamicHDRPolicy {
	case "", "reject", "verify":
	default:
		return fmt.Errorf("unsupported dynamic HDR policy")
	}
	if in.DynamicHDRPolicy != "" && in.HDRPolicy != "preserve" && !in.PreserveHDR {
		return fmt.Errorf("dynamic HDR policy requires HDR preservation")
	}
	return nil
}

func validateInputMediaPolicy(input mediaProbe, in Invocation) error {
	if in.DynamicHDRPolicy != "reject" {
		return nil
	}
	for _, stream := range input.Streams {
		if strings.EqualFold(stream.CodecType, "video") {
			_, dynamic := stream.HDR()
			if dynamic {
				return fmt.Errorf("source dynamic HDR is unsupported by this profile")
			}
		}
	}
	return nil
}

func mediaPolicyArguments(in Invocation) []string {
	args := []string{}
	switch in.StreamSelection {
	case "all":
		args = append(args, "-map", "0")
	case "first-video-audio":
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?")
	case "first-video":
		args = append(args, "-map", "0:v:0?")
	case "audio":
		args = append(args, "-map", "0:a:0?")
	}
	if in.PreserveMetadata {
		args = append(args, "-map_metadata", "0", "-map_chapters", "0")
	}
	return args
}

func validateStreamSelection(input, output mediaProbe, policy string) error {
	if policy == "" {
		return nil
	}
	inputStreams, outputStreams := input.Streams, output.Streams
	filter := func(streams []mediaStream, kind string) []mediaStream {
		out := []mediaStream{}
		for _, stream := range streams {
			if strings.EqualFold(stream.CodecType, kind) {
				out = append(out, stream)
			}
		}
		return out
	}
	switch policy {
	case "all":
		if len(inputStreams) != len(outputStreams) {
			return fmt.Errorf("output did not preserve all source streams")
		}
		for i := range inputStreams {
			if !strings.EqualFold(inputStreams[i].CodecType, outputStreams[i].CodecType) {
				return fmt.Errorf("output changed source stream layout")
			}
			if inputStreams[i].CodecType == "audio" && (inputStreams[i].Channels != outputStreams[i].Channels || inputStreams[i].ChannelLayout != outputStreams[i].ChannelLayout) {
				return fmt.Errorf("output changed source audio channel layout")
			}
		}
	case "first-video-audio":
		if len(filter(inputStreams, "video")) > 0 && len(filter(outputStreams, "video")) != 1 || len(filter(inputStreams, "audio")) > 0 && len(filter(outputStreams, "audio")) != 1 {
			return fmt.Errorf("output did not preserve the selected video/audio streams")
		}
	case "first-video":
		if len(filter(inputStreams, "video")) > 0 && len(filter(outputStreams, "video")) != 1 {
			return fmt.Errorf("output did not preserve the selected video stream")
		}
	case "audio":
		if len(filter(inputStreams, "audio")) > 0 && len(filter(outputStreams, "audio")) != 1 {
			return fmt.Errorf("output did not preserve the selected audio stream")
		}
	}
	return nil
}

func validateMetadata(input, output mediaProbe) error {
	for key, value := range input.FormatTags {
		if metadataComparable(key) && output.FormatTags[key] != value {
			return fmt.Errorf("output lost source metadata %s", key)
		}
	}
	used := make([]bool, len(output.Streams))
	for _, source := range input.Streams {
		var target *mediaStream
		for i := range output.Streams {
			if !used[i] && output.Streams[i].Index == source.Index && strings.EqualFold(output.Streams[i].CodecType, source.CodecType) {
				used[i] = true
				target = &output.Streams[i]
				break
			}
		}
		if target == nil {
			for i := range output.Streams {
				if !used[i] && strings.EqualFold(output.Streams[i].CodecType, source.CodecType) {
					used[i] = true
					target = &output.Streams[i]
					break
				}
			}
		}
		if target == nil {
			continue
		}
		for key, value := range source.Tags {
			if metadataComparable(key) && target.Tags[key] != value {
				return fmt.Errorf("output lost source stream metadata %s", key)
			}
		}
	}
	return nil
}

func metadataComparable(key string) bool {
	key = strings.ToLower(key)
	return key != "encoder" && key != "major_brand" && key != "minor_version" && key != "compatible_brands"
}

func sameDynamicSideData(source, target mediaStream) bool {
	canonical := func(stream mediaStream) map[string]int {
		out := map[string]int{}
		for _, data := range stream.SideData {
			lower := strings.ToLower(data.Type)
			if strings.Contains(lower, "dynamic") || strings.Contains(lower, "hdr10+") || strings.Contains(lower, "dolby vision") || strings.Contains(lower, "dovi") {
				encoded, _ := json.Marshal(data.Values)
				out[data.Type+":"+string(encoded)]++
			}
		}
		return out
	}
	sourceData, targetData := canonical(source), canonical(target)
	if len(sourceData) != len(targetData) {
		return false
	}
	for key, count := range sourceData {
		if targetData[key] != count {
			return false
		}
	}
	return true
}
