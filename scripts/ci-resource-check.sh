#!/usr/bin/env bash
set -euo pipefail

minimum_disk_gib="${1:-4}"
minimum_memory_mib="${2:-2048}"
workspace="${GITHUB_WORKSPACE:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
[[ "$minimum_disk_gib" =~ ^[1-9][0-9]*$ && "$minimum_memory_mib" =~ ^[1-9][0-9]*$ ]] || {
  echo 'Usage: scripts/ci-resource-check.sh MIN_DISK_GIB MIN_AVAILABLE_MEMORY_MIB' >&2
  exit 2
}

disk_kib="$(df -Pk "$workspace" | awk 'NR==2 {print $4}')"
memory_kib="$(awk '/MemAvailable:/ {print $2}' /proc/meminfo)"
if [[ -r /sys/fs/cgroup/memory.max && -r /sys/fs/cgroup/memory.current ]]; then
  cgroup_limit="$(cat /sys/fs/cgroup/memory.max)"
  cgroup_used="$(cat /sys/fs/cgroup/memory.current)"
  if [[ "$cgroup_limit" =~ ^[0-9]+$ ]]; then
    cgroup_available=$(((cgroup_limit - cgroup_used) / 1024))
    (( cgroup_available < memory_kib )) && memory_kib="$cgroup_available"
  fi
fi
cpus="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 1)"
required_kib=$((minimum_disk_gib * 1024 * 1024))
required_mem_kib=$((minimum_memory_mib * 1024))

printf 'Runner resources: workspace=%s (%s KiB free), available-memory=%s MiB, CPUs=%s\n' \
  "$workspace" "$disk_kib" "$((memory_kib / 1024))" "$cpus"
df -h "$workspace"

if [[ "$disk_kib" -lt "$required_kib" ]]; then
  echo "Insufficient workspace disk: ${disk_kib} KiB free; ${required_kib} KiB required (${minimum_disk_gib} GiB)." >&2
  exit 2
fi
if [[ "$memory_kib" -lt "$required_mem_kib" ]]; then
  echo "Insufficient available memory: ${memory_kib} KiB; ${required_mem_kib} KiB required (${minimum_memory_mib} MiB)." >&2
  exit 2
fi

if docker info >/dev/null 2>&1; then
  docker_root="$(docker info --format '{{.DockerRootDir}}')"
  daemon_memory="$(docker info --format '{{.MemTotal}}')"
  printf 'Docker daemon: root=%s total-memory=%s MiB\n' "$docker_root" "$((daemon_memory / 1024 / 1024))"
  df -h "$docker_root"
  docker system df
fi
