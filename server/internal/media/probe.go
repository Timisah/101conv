// Package media contains the bounded, typed media analysis contract shared by
// the Worker and Core inspection paths. It never exposes raw tool stderr and
// never accepts a shell command.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ProbeTimeout   = 30 * time.Second
	MaxProbeOutput = 32 << 20
)

var pixelBitDepthRE = regexp.MustCompile(`p([0-9]{2})(?:le|be)?$`)

type Probe struct {
	Filename   string            `json:"filename,omitempty"`
	StartTime  float64           `json:"startTime,omitempty"`
	Size       float64           `json:"size,omitempty"`
	ProbeScore int               `json:"probeScore,omitempty"`
	Duration   float64           `json:"duration,omitempty"`
	BitRate    float64           `json:"bitRate,omitempty"`
	Format     string            `json:"format,omitempty"`
	LongName   string            `json:"longName,omitempty"`
	FormatTags map[string]string `json:"formatTags,omitempty"`
	Streams    []Stream          `json:"streams"`
	Programs   []Program         `json:"programs,omitempty"`
	Chapters   []Chapter         `json:"chapters,omitempty"`
	// RawJSON is the bounded, complete ffprobe document. It is returned by
	// inspection to the authorized requester so format-specific metadata is not
	// silently discarded; the Worker does not publish it.
	RawJSON json.RawMessage `json:"rawJson,omitempty"`
}

type Chapter struct {
	ID        int64             `json:"id"`
	StartTime float64           `json:"startTime,omitempty"`
	EndTime   float64           `json:"endTime,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
}

type Program struct {
	ID            int64             `json:"id"`
	ProgramNumber int64             `json:"programNumber,omitempty"`
	PMTPID        int64             `json:"pmtPid,omitempty"`
	PCRPID        int64             `json:"pcrPid,omitempty"`
	Name          string            `json:"name,omitempty"`
	StartTime     float64           `json:"startTime,omitempty"`
	EndTime       float64           `json:"endTime,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
	StreamIndexes []int             `json:"streamIndexes,omitempty"`
}

type Stream struct {
	Index              int               `json:"index"`
	ID                 string            `json:"id,omitempty"`
	CodecType          string            `json:"codec_type"`
	CodecName          string            `json:"codec_name,omitempty"`
	CodecLongName      string            `json:"codec_long_name,omitempty"`
	Profile            string            `json:"profile,omitempty"`
	Level              int               `json:"level,omitempty"`
	CodecTagString     string            `json:"codec_tag_string,omitempty"`
	CodecTimeBase      string            `json:"codecTimeBase,omitempty"`
	TimeBase           string            `json:"timeBase,omitempty"`
	Width              int               `json:"width,omitempty"`
	Height             int               `json:"height,omitempty"`
	SampleAspectRatio  string            `json:"sampleAspectRatio,omitempty"`
	DisplayAspectRatio string            `json:"displayAspectRatio,omitempty"`
	FieldOrder         string            `json:"fieldOrder,omitempty"`
	ColorPrimaries     string            `json:"color_primaries,omitempty"`
	ColorTransfer      string            `json:"color_transfer,omitempty"`
	ColorSpace         string            `json:"color_space,omitempty"`
	ColorRange         string            `json:"color_range,omitempty"`
	BitsPerRawSample   string            `json:"bits_per_raw_sample,omitempty"`
	PixelFormat        string            `json:"pix_fmt,omitempty"`
	Channels           int               `json:"channels,omitempty"`
	ChannelLayout      string            `json:"channel_layout,omitempty"`
	SampleRate         string            `json:"sample_rate,omitempty"`
	FrameRate          string            `json:"r_frame_rate,omitempty"`
	StartPTS           int64             `json:"startPts,omitempty"`
	StartTime          float64           `json:"startTime,omitempty"`
	DurationTS         int64             `json:"durationTs,omitempty"`
	BitRate            float64           `json:"bitRate,omitempty"`
	Duration           float64           `json:"duration,omitempty"`
	NbFrames           int64             `json:"nbFrames,omitempty"`
	NBReadFrames       int64             `json:"nbReadFrames,omitempty"`
	ExtradataSize      int               `json:"extradataSize,omitempty"`
	Disposition        map[string]int    `json:"disposition,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
	SideData           []SideData        `json:"sideData,omitempty"`
}

type SideData struct {
	Type   string                     `json:"side_data_type"`
	Values map[string]json.RawMessage `json:"values,omitempty"`
}

type probeDocument struct {
	Format struct {
		Filename   string            `json:"filename"`
		Name       string            `json:"format_name"`
		LongName   string            `json:"format_long_name"`
		StartTime  json.RawMessage   `json:"start_time"`
		Duration   json.RawMessage   `json:"duration"`
		BitRate    json.RawMessage   `json:"bit_rate"`
		Size       json.RawMessage   `json:"size"`
		ProbeScore int               `json:"probe_score"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
	Streams  []probeStream  `json:"streams"`
	Programs []probeProgram `json:"programs"`
	Chapters []probeChapter `json:"chapters"`
}

type probeStream struct {
	Index              int                          `json:"index"`
	ID                 string                       `json:"id"`
	CodecType          string                       `json:"codec_type"`
	CodecName          string                       `json:"codec_name"`
	CodecLongName      string                       `json:"codec_long_name"`
	Profile            string                       `json:"profile"`
	Level              int                          `json:"level"`
	CodecTagString     string                       `json:"codec_tag_string"`
	CodecTimeBase      string                       `json:"codec_time_base"`
	TimeBase           string                       `json:"time_base"`
	Width              int                          `json:"width"`
	Height             int                          `json:"height"`
	SampleAspectRatio  string                       `json:"sample_aspect_ratio"`
	DisplayAspectRatio string                       `json:"display_aspect_ratio"`
	FieldOrder         string                       `json:"field_order"`
	ColorPrimaries     string                       `json:"color_primaries"`
	ColorTransfer      string                       `json:"color_transfer"`
	ColorSpace         string                       `json:"color_space"`
	ColorRange         string                       `json:"color_range"`
	BitsPerRawSample   string                       `json:"bits_per_raw_sample"`
	PixelFormat        string                       `json:"pix_fmt"`
	Channels           int                          `json:"channels"`
	ChannelLayout      string                       `json:"channel_layout"`
	SampleRate         string                       `json:"sample_rate"`
	FrameRate          string                       `json:"r_frame_rate"`
	StartPTS           int64                        `json:"start_pts"`
	StartTime          json.RawMessage              `json:"start_time"`
	DurationTS         int64                        `json:"duration_ts"`
	BitRate            json.RawMessage              `json:"bit_rate"`
	Duration           json.RawMessage              `json:"duration"`
	NbFrames           json.RawMessage              `json:"nb_frames"`
	NBReadFrames       json.RawMessage              `json:"nb_read_frames"`
	ExtradataSize      int                          `json:"extradata_size"`
	Disposition        map[string]int               `json:"disposition"`
	Tags               map[string]string            `json:"tags"`
	SideData           []map[string]json.RawMessage `json:"side_data_list"`
}

type probeProgram struct {
	ID            int64             `json:"program_id"`
	ProgramNumber int64             `json:"program_num"`
	PMTPID        int64             `json:"pmt_pid"`
	PCRPID        int64             `json:"pcr_pid"`
	Name          string            `json:"program_name"`
	StartTime     json.RawMessage   `json:"start_time"`
	EndTime       json.RawMessage   `json:"end_time"`
	Tags          map[string]string `json:"tags"`
	Streams       []probeStream     `json:"streams"`
}

type probeChapter struct {
	ID        int64             `json:"id"`
	StartTime json.RawMessage   `json:"start_time"`
	EndTime   json.RawMessage   `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

type boundedBuffer struct {
	data      []byte
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := MaxProbeOutput - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			b.data = append(b.data, p[:remaining]...)
			b.truncated = true
		} else {
			b.data = append(b.data, p...)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.data }

// ProbeFile executes only the already-resolved probe executable. Callers that
// resolve paths from an installed engine must validate that path first.
func ProbeFile(parent context.Context, executable, work, inputPath string) (*Probe, error) {
	if strings.TrimSpace(executable) == "" || strings.TrimSpace(inputPath) == "" {
		return nil, fmt.Errorf("media probe paths are required")
	}
	ctx, cancel := context.WithTimeout(parent, ProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-v", "error", "-show_private_data", "1", "-show_streams", "-show_programs", "-show_format", "-show_chapters", "-of", "json", inputPath)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work, "LANG=C.UTF-8"}
	stdout, stderr := &boundedBuffer{}, &boundedBuffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("media probe timed out")
		}
		return nil, fmt.Errorf("media probe failed")
	}
	if stdout.truncated {
		return nil, fmt.Errorf("media probe metadata exceeds the inspection limit")
	}
	var document probeDocument
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		return nil, fmt.Errorf("media probe returned invalid metadata")
	}
	out := &Probe{Filename: document.Format.Filename, Format: document.Format.Name, LongName: document.Format.LongName, FormatTags: allTags(document.Format.Tags), ProbeScore: document.Format.ProbeScore, RawJSON: append(json.RawMessage(nil), stdout.Bytes()...)}
	out.StartTime, _ = ParseNumber(document.Format.StartTime)
	out.Duration, _ = ParseNumber(document.Format.Duration)
	out.BitRate, _ = ParseNumber(document.Format.BitRate)
	out.Size, _ = ParseNumber(document.Format.Size)
	for _, stream := range document.Streams {
		parsed := Stream{Index: stream.Index, ID: stream.ID, CodecType: stream.CodecType, CodecName: stream.CodecName, CodecLongName: stream.CodecLongName, Profile: stream.Profile, Level: stream.Level, CodecTagString: stream.CodecTagString, CodecTimeBase: stream.CodecTimeBase, TimeBase: stream.TimeBase, Width: stream.Width, Height: stream.Height, SampleAspectRatio: stream.SampleAspectRatio, DisplayAspectRatio: stream.DisplayAspectRatio, FieldOrder: stream.FieldOrder, ColorPrimaries: stream.ColorPrimaries, ColorTransfer: stream.ColorTransfer, ColorSpace: stream.ColorSpace, ColorRange: stream.ColorRange, BitsPerRawSample: stream.BitsPerRawSample, PixelFormat: stream.PixelFormat, Channels: stream.Channels, ChannelLayout: stream.ChannelLayout, SampleRate: stream.SampleRate, FrameRate: stream.FrameRate, StartPTS: stream.StartPTS, DurationTS: stream.DurationTS, ExtradataSize: stream.ExtradataSize, Disposition: stream.Disposition, Tags: allTags(stream.Tags)}
		parsed.BitRate, _ = ParseNumber(stream.BitRate)
		parsed.Duration, _ = ParseNumber(stream.Duration)
		parsed.StartTime, _ = ParseNumber(stream.StartTime)
		parsed.NbFrames, _ = ParseNumberInt(stream.NbFrames)
		parsed.NBReadFrames, _ = ParseNumberInt(stream.NBReadFrames)
		for _, raw := range stream.SideData {
			typeName := ""
			values := make(map[string]json.RawMessage)
			for key, value := range raw {
				if key == "side_data_type" {
					_ = json.Unmarshal(value, &typeName)
					continue
				}
				values[key] = append(json.RawMessage(nil), value...)
			}
			parsed.SideData = append(parsed.SideData, SideData{Type: typeName, Values: values})
		}
		out.Streams = append(out.Streams, parsed)
	}
	for _, program := range document.Programs {
		parsed := Program{ID: program.ID, ProgramNumber: program.ProgramNumber, PMTPID: program.PMTPID, PCRPID: program.PCRPID, Name: program.Name, Tags: allTags(program.Tags)}
		parsed.StartTime, _ = ParseNumber(program.StartTime)
		parsed.EndTime, _ = ParseNumber(program.EndTime)
		for _, stream := range program.Streams {
			parsed.StreamIndexes = append(parsed.StreamIndexes, stream.Index)
		}
		out.Programs = append(out.Programs, parsed)
	}
	for _, chapter := range document.Chapters {
		start, _ := ParseNumber(chapter.StartTime)
		end, _ := ParseNumber(chapter.EndTime)
		out.Chapters = append(out.Chapters, Chapter{ID: chapter.ID, StartTime: start, EndTime: end, Tags: allTags(chapter.Tags)})
	}
	return out, nil
}

func ParseNumber(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var text string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
	} else {
		text = string(raw)
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, fmt.Errorf("invalid probe number")
	}
	return number, nil
}

func ParseNumberInt(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var text string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
	} else {
		text = string(raw)
	}
	value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func (s Stream) BitDepth() int {
	if value, err := strconv.Atoi(strings.TrimSpace(s.BitsPerRawSample)); err == nil && value > 0 {
		return value
	}
	match := pixelBitDepthRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s.PixelFormat)))
	if len(match) == 2 {
		value, _ := strconv.Atoi(match[1])
		return value
	}
	return 0
}

func (s Stream) SideDataContains(needle string) bool {
	needle = strings.ToLower(needle)
	for _, data := range s.SideData {
		if strings.Contains(strings.ToLower(data.Type), needle) {
			return true
		}
	}
	return false
}

func (s Stream) HDR() (isHDR, dynamic bool) {
	transfer := strings.ToLower(strings.TrimSpace(s.ColorTransfer))
	primaries := strings.ToLower(strings.TrimSpace(s.ColorPrimaries))
	bitDepth := s.BitDepth()
	isHDR = transfer == "smpte2084" || transfer == "arib-std-b67" || s.SideDataContains("mastering display metadata") || s.SideDataContains("content light level metadata") || primaries == "bt2020" && bitDepth >= 10
	dynamic = s.SideDataContains("dynamic") || s.SideDataContains("hdr10+") || s.SideDataContains("dolby vision") || s.SideDataContains("dovi")
	return isHDR, dynamic
}

func FirstVideoStream(probe Probe) (Stream, bool) {
	for _, stream := range probe.Streams {
		if strings.EqualFold(stream.CodecType, "video") {
			return stream, true
		}
	}
	return Stream{}, false
}

func allTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	all := make(map[string]string, len(tags))
	for key, value := range tags {
		all[key] = value
	}
	return all
}
