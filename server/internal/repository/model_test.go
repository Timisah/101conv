package repository

import (
	"encoding/json"
	"testing"
)

func TestIndexSupportsSeparatedEnginesAndProfilesAndLegacyProfileOnlyIndexes(t *testing.T) {
	var index Index
	data := []byte(`{"version":1,"engines":[{"id":"ffmpeg","version":"9.0.0","name":"FFmpeg","manifestUrl":"https://example.test/ffmpeg.json"}],"profiles":[{"id":"video-mp4","version":"1.0.0","name":"Video MP4","manifestUrl":"https://example.test/video-mp4.json"}]}`)
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Engines) != 1 || len(index.Profiles) != 1 || index.Engines[0].ID != "ffmpeg" {
		t.Fatalf("unexpected separated index: %+v", index)
	}

	var legacy Index
	if err := json.Unmarshal([]byte(`{"version":1,"profiles":[{"id":"old","version":"1.0.0","name":"Old profile","manifestUrl":"https://example.test/old.json"}]}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy.Engines) != 0 || len(legacy.Profiles) != 1 {
		t.Fatalf("legacy profile-only index did not remain readable: %+v", legacy)
	}
}
