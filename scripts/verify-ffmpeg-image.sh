#!/usr/bin/env bash
set -euo pipefail

# Exercise the exact FFmpeg artifact named by the catalog engine manifest. The
# archive is cached by CI, but every use still checks the pinned length and
# SHA-256 before extraction.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
manifest="$repo_root/catalog/targets/engines/ffmpeg-linux-amd64.json"
read -r download_url expected_sha expected_size < <(python3 - "$manifest" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    artifact = json.load(f)["engine"]["artifact"]
print(artifact["downloadUrl"], artifact["sha256"], artifact["sizeBytes"])
PY
)

cache_dir="${FFMPEG_ARTIFACT_CACHE:-${RUNNER_TEMP:-/tmp}/101conv-ffmpeg-cache}"
mkdir -p "$cache_dir"
artifact="$cache_dir/ffmpeg-$expected_sha.tar.xz"
downloaded="$artifact.download.$$"
valid_cached=false
if [[ -f "$artifact" ]]; then
    [[ "$(stat -c '%s' "$artifact")" == "$expected_size" ]] &&
        [[ "$(sha256sum "$artifact" | awk '{print $1}')" == "$expected_sha" ]] && valid_cached=true
fi
if [[ "$valid_cached" != true ]]; then
    echo "Downloading and verifying pinned FFmpeg artifact..."
    curl --fail --location --retry 3 --retry-delay 2 --output "$downloaded" "$download_url"
    test "$(stat -c '%s' "$downloaded")" = "$expected_size"
    test "$(sha256sum "$downloaded" | awk '{print $1}')" = "$expected_sha"
    mv "$downloaded" "$artifact"
fi

fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/101conv-ffmpeg-image.XXXXXX")"
trap 'rm -rf "$fixture_root" "$downloaded"' EXIT
tar -xJf "$artifact" -C "$fixture_root"
ffmpeg_bin="$(find "$fixture_root" -type f -name ffmpeg -print -quit)"
ffprobe_bin="$(find "$fixture_root" -type f -name ffprobe -print -quit)"
test -x "$ffmpeg_bin" -a -x "$ffprobe_bin"

"$ffmpeg_bin" -hide_banner -loglevel error -f lavfi -i 'testsrc2=size=160x96:rate=2' -frames:v 1 -c:v png "$fixture_root/input.png"
"$ffmpeg_bin" -hide_banner -loglevel error -i "$fixture_root/input.png" -frames:v 1 -c:v mjpeg -q:v 3 "$fixture_root/input.jpg"
"$ffmpeg_bin" -hide_banner -loglevel error -i "$fixture_root/input.png" -frames:v 1 -c:v libwebp -quality 75 "$fixture_root/input.webp"
"$ffmpeg_bin" -hide_banner -loglevel error -i "$fixture_root/input.png" -frames:v 1 -c:v tiff "$fixture_root/input.tiff"
"$ffmpeg_bin" -hide_banner -loglevel error -i "$fixture_root/input.png" -frames:v 1 -c:v bmp "$fixture_root/input.bmp"
"$ffmpeg_bin" -hide_banner -loglevel error -f lavfi -i 'testsrc2=size=160x96:rate=2' -t 1 -c:v gif "$fixture_root/input.gif"
"$ffmpeg_bin" -hide_banner -loglevel error -i "$fixture_root/input.png" -frames:v 1 -c:v libaom-av1 -crf 30 -b:v 0 -still-picture 1 "$fixture_root/input.avif"

validate_signature() {
    python3 - "$1" "$2" <<'PY'
import sys
path, extension = sys.argv[1:]
with open(path, "rb") as f:
    header = f.read(12)
checks = {
    "jpg": header[:3] == b"\xff\xd8\xff",
    "jpeg": header[:3] == b"\xff\xd8\xff",
    "png": header[:8] == b"\x89PNG\r\n\x1a\n",
    "webp": header[:4] == b"RIFF" and header[8:12] == b"WEBP",
    "tif": header[:4] in (b"II*\x00", b"MM\x00*", b"II+\x00", b"MM\x00+"),
    "tiff": header[:4] in (b"II*\x00", b"MM\x00*", b"II+\x00", b"MM\x00+"),
    "bmp": header[:2] == b"BM",
    "gif": header[:6] in (b"GIF87a", b"GIF89a"),
    "avif": header[4:8] == b"ftyp" and header[8:12] in (b"avif", b"avis"),
}
if not checks[extension]:
    raise SystemExit(f"bad {extension} signature")
PY
}

sources=(jpg png webp tiff bmp gif avif)
targets=(jpg jpeg png webp tif tiff bmp gif avif)
for source in "${sources[@]}"; do
    for target in "${targets[@]}"; do
        output="$fixture_root/${source}-to-${target}.${target}"
        args=(-hide_banner -loglevel error -nostdin -y -i "$fixture_root/input.$source" -map_metadata 0 -vf 'scale=64:64:force_original_aspect_ratio=decrease')
        case "$target" in
            jpg|jpeg) args+=(-frames:v 1 -c:v mjpeg -q:v 3) ;;
            png) args+=(-frames:v 1 -c:v png -compression_level 6) ;;
            webp) args+=(-c:v libwebp -quality 75) ;;
            tif|tiff) args+=(-frames:v 1 -c:v tiff) ;;
            bmp) args+=(-frames:v 1 -c:v bmp) ;;
            gif) args+=(-c:v gif) ;;
            avif) args+=(-frames:v 1 -c:v libaom-av1 -crf 30 -b:v 0 -still-picture 1) ;;
        esac
        "$ffmpeg_bin" "${args[@]}" "$output"
        test -s "$output"
        validate_signature "$output" "$target"
        dimensions="$("$ffprobe_bin" -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$output")"
        test "$dimensions" = "64,38"
        if [[ "$source" == gif && "$target" == webp ]]; then
            frames="$("$ffprobe_bin" -v error -count_frames -select_streams v:0 -show_entries stream=nb_read_frames -of csv=p=0 "$output")"
            test "$frames" -ge 2
        fi
        if [[ "$source" == gif && "$target" == gif ]]; then
            frames="$("$ffprobe_bin" -v error -count_frames -select_streams v:0 -show_entries stream=nb_read_frames -of csv=p=0 "$output")"
            test "$frames" -ge 2
        fi
        if [[ "$source" == gif && "$target" == avif ]]; then
            frames="$("$ffprobe_bin" -v error -count_frames -select_streams v:0 -show_entries stream=nb_read_frames -of csv=p=0 "$output")"
            test "$frames" = 1
        fi
    done
done

printf 'not an image\n' > "$fixture_root/corrupt.jpg"
if "$ffmpeg_bin" -hide_banner -loglevel error -nostdin -y -i "$fixture_root/corrupt.jpg" -frames:v 1 -c:v png "$fixture_root/corrupt.png" 2>/dev/null; then
    echo 'corrupt input unexpectedly converted' >&2
    exit 1
fi

echo "Pinned FFmpeg image matrix passed: ${#sources[@]} inputs x ${#targets[@]} targets; animated GIF behavior, dimensions, signatures, and corrupt-input failure verified."
