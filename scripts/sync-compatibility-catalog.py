#!/usr/bin/env python3
"""Generate and verify the legacy compatibility catalog from the target catalog.

The signed target catalog under catalog/targets is authoritative.  The small
compatibility index at catalog/index.json and the copy embedded in Core are
derived views for older clients and the first-run API.  This script never
fetches remote data and refuses unsafe manifest targets.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
TARGET_INDEX = ROOT / "catalog" / "targets" / "catalog" / "index.json"
COMPATIBILITY_INDEX = ROOT / "catalog" / "index.json"
EMBEDDED_INDEX = ROOT / "server" / "internal" / "catalog" / "official.json"
PUBLIC_BASE = "https://raw.githubusercontent.com/Timisah/101conv/main/catalog"
LEGACY_MANIFESTS = (
    ROOT / "catalog" / "profiles" / "ffmpeg-linux-amd64.json",
    ROOT / "catalog" / "profiles" / "jpeg-xl-linux-amd64.json",
)


def load_json(path: Path) -> dict:
    try:
        with path.open(encoding="utf-8") as stream:
            value = json.load(stream)
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"read {path.relative_to(ROOT)}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"{path.relative_to(ROOT)} must contain a JSON object")
    return value


def safe_target(value: str) -> bool:
    path = Path(value)
    return bool(value) and not path.is_absolute() and "\\" not in value and all(
        part not in ("", ".", "..") for part in value.split("/")
    )


def manifest(target: str, kind: str) -> dict:
    if not safe_target(target):
        raise ValueError(f"unsafe {kind} manifest target {target!r}")
    path = ROOT / "catalog" / "targets" / target
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"missing or non-regular {kind} manifest {target!r}")
    value = load_json(path)
    payload = value.get(kind)
    if not isinstance(payload, dict):
        raise ValueError(f"{target!r} does not contain a {kind} manifest")
    return payload


def required_text(value: object, label: str, target: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{target!r} has no {label}")
    return value


def reference(entry: dict, kind: str, catalog_bounds: tuple[str, str]) -> dict:
    target = required_text(entry.get("manifestTarget"), "manifestTarget", "catalog index entry")
    payload = manifest(target, kind)
    entry_id = required_text(entry.get("id"), "id", target)
    entry_version = required_text(entry.get("version"), "version", target)
    for field in ("id", "version", "name"):
        if payload.get(field) != entry.get(field):
            raise ValueError(
                f"{target!r} disagrees with catalog entry for {field}: "
                f"{payload.get(field)!r} != {entry.get(field)!r}"
            )
    result = {
        "id": entry_id,
        "version": entry_version,
        "name": required_text(payload.get("name"), "name", target),
        "description": payload.get("description", ""),
        "manifestUrl": f"{PUBLIC_BASE}/targets/{target}",
    }
    if kind == "engine":
        result["minimumAppVersion"], result["maximumAppVersionExclusive"] = catalog_bounds
    else:
        result["minimumAppVersion"] = payload.get("minimumAppVersion", "")
        result["maximumAppVersionExclusive"] = payload.get("maximumAppVersionExclusive", "")
    return {key: value for key, value in result.items() if value != ""}


def expected_index() -> dict:
    target_index = load_json(TARGET_INDEX)
    if target_index.get("schemaVersion") != 1:
        raise ValueError("target catalog must use schema version 1")
    minimum = required_text(target_index.get("minimumAppVersion"), "minimumAppVersion", "target catalog")
    maximum = required_text(
        target_index.get("maximumAppVersionExclusive"),
        "maximumAppVersionExclusive",
        "target catalog",
    )
    engines = [reference(entry, "engine", (minimum, maximum)) for entry in target_index.get("engines", [])]
    profiles = [reference(entry, "profile", (minimum, maximum)) for entry in target_index.get("profiles", [])]
    if not engines or not profiles:
        raise ValueError("target catalog must contain engines and profiles")
    for kind, entries in (("engine", engines), ("profile", profiles)):
        seen = set()
        for entry in entries:
            identity = (entry["id"], entry["version"])
            if identity in seen:
                raise ValueError(f"duplicate {kind} release {identity[0]} {identity[1]}")
            seen.add(identity)
    return {
        "version": 1,
        "minimumAppVersion": minimum,
        "maximumAppVersionExclusive": maximum,
        "engines": engines,
        "profiles": profiles,
    }


def encoded(value: dict) -> str:
    return json.dumps(value, indent=2, ensure_ascii=False) + "\n"


def check(expected: dict) -> list[str]:
    errors: list[str] = []
    expected_text = encoded(expected)
    for path in (COMPATIBILITY_INDEX, EMBEDDED_INDEX):
        try:
            actual = load_json(path)
        except ValueError as exc:
            errors.append(str(exc))
            continue
        if encoded(actual) != expected_text:
            errors.append(f"{path.relative_to(ROOT)} is out of sync with the target catalog")
    for path in LEGACY_MANIFESTS:
        if not path.is_file():
            errors.append(f"legacy compatibility manifest is missing: {path.relative_to(ROOT)}")
    return errors


def write(expected: dict) -> None:
    value = encoded(expected)
    for path in (COMPATIBILITY_INDEX, EMBEDDED_INDEX):
        path.write_text(value, encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--write", action="store_true", help="write both derived compatibility indexes")
    parser.add_argument("--check", action="store_true", help="verify both derived compatibility indexes (default)")
    args = parser.parse_args()
    try:
        expected = expected_index()
        errors = check(expected)
        if args.write:
            write(expected)
            errors = check(expected)
        if errors:
            for error in errors:
                print(f"catalog drift: {error}", file=sys.stderr)
            print("Run scripts/sync-compatibility-catalog.py --write after reviewing target manifests.", file=sys.stderr)
            return 1
        print(
            f"Compatibility catalog is synchronized: {len(expected['engines'])} engines, "
            f"{len(expected['profiles'])} profiles."
        )
        return 0
    except ValueError as exc:
        print(f"catalog validation failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
