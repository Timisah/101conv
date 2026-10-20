#!/usr/bin/env bash
set -euo pipefail

# Static Compose security contract. This resolves variables with disposable
# placeholders and never prints the resulting environment or credentials.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
config_file="$(mktemp)"
trap 'rm -f "$config_file"' EXIT

POSTGRES_PASSWORD=p0-audit-only \
ENGINE_ADMIN_TOKEN=e0-audit-only \
DATA_PATH=/tmp/101conv-security-audit \
PORT=5656 \
docker compose -f "$repo_root/compose.yaml" config --format json >"$config_file"

python3 - "$config_file" <<'PY'
import json
import sys

config = json.load(open(sys.argv[1], encoding="utf-8"))
services = config["services"]
networks = config["networks"]

def fail(message):
    raise SystemExit(f"Compose security audit failed: {message}")

def require(condition, message):
    if not condition:
        fail(message)

for name in ("core", "web", "worker"):
    service = services[name]
    require(service.get("read_only") is True, f"{name} must use a read-only root filesystem")
    require("ALL" in service.get("cap_drop", []), f"{name} must drop all Linux capabilities")
    require(service.get("privileged") is not True, f"{name} must not run privileged")
    require(not any("docker.sock" in str(volume) for volume in service.get("volumes", [])), f"{name} must not mount Docker socket")

require(services["core"].get("user") == "65532:65532", "Core must run as the unprivileged application UID")
require(services["worker"].get("user") == "65532:65532", "Worker must run as the unprivileged application UID")
require(services["web"].get("user") == "101:101", "Web must run as the unprivileged Nginx UID")

require(not services["core"].get("ports"), "Core must not publish a host port")
require(not services["worker"].get("ports"), "Worker must not publish a host port")
web_ports = services["web"].get("ports", [])
require(len(web_ports) == 1 and web_ports[0].get("target") == 5656, "Web must publish only its configured ingress port")

require(networks["core-db"].get("internal") is True, "Core database network must be internal")
require(networks["worker-db"].get("internal") is True, "Worker database network must be internal")
require(set(services["worker"].get("networks", {})) == {"worker-db"}, "Worker must have only its internal database network")
require("web-core" in services["core"].get("networks", {}), "Core must be reachable from the Web ingress network")
require("web-core" in services["web"].get("networks", {}), "Web must use the Core ingress network")

def volume(service, target):
    for value in services[service].get("volumes", []):
        if value.get("target") == target:
            return value
    fail(f"{service} is missing volume {target}")

require(volume("worker", "/var/lib/101conv/engines").get("read_only") is True, "Worker engine files must be read-only")
require(volume("worker", "/var/lib/101conv/uploads").get("read_only") is True, "Worker uploads must be read-only")
require(volume("worker", "/var/lib/101conv/results").get("read_only") is not True, "Worker results must remain writable for outputs")
require(not any(value.get("target") == "/mounts" for value in services["worker"].get("volumes", [])), "Worker must not mount automation folders")

limits = {
    "core": (128, 805306368, 1.5),
    "web": (64, 134217728, 0.5),
    "worker": (64, 1073741824, 1.0),
}
for name, (pids, memory, cpus) in limits.items():
    service = services[name]
    require(service.get("pids_limit") == pids, f"{name} pids_limit changed from the reviewed bound")
    require(int(service.get("mem_limit", 0)) == memory, f"{name} mem_limit changed from the reviewed bound")
    require(float(service.get("cpus")) == cpus, f"{name} cpus changed from the reviewed bound")

print("Compose security contract passed: non-root app services, dropped capabilities, internal Worker network, restricted mounts, no host Worker/Core ports, and resource bounds verified.")
PY
