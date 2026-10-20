#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
command -v docker >/dev/null || { echo 'Docker is required.' >&2; exit 1; }
docker compose version >/dev/null || { echo 'Docker Compose is required.' >&2; exit 1; }

if [[ ! -f .env ]]; then
  if [[ -e data/go-data ]]; then
    echo 'data/go-data already exists. Configure the existing installation explicitly; this installer will not adopt or overwrite it.' >&2
    exit 1
  fi
  command -v openssl >/dev/null || { echo 'OpenSSL is required to create installation secrets.' >&2; exit 1; }
  umask 077
  mkdir -p data/go-data/engines data/go-data/imports
  postgres_secret="$(openssl rand -hex 32)"
  admin_secret="$(openssl rand -hex 32)"
  worker_secret="$(openssl rand -hex 32)"
  env_tmp="$(mktemp "$repo_root/.env.XXXXXXXX")"
  trap 'rm -f "$env_tmp"' EXIT
  {
    printf 'DATA_PATH=%s\n' "$repo_root/data"
    printf 'POSTGRES_PASSWORD=%s\n' "$postgres_secret"
    printf 'ENGINE_ADMIN_TOKEN=%s\n' "$admin_secret"
    printf 'POSTGRES_WORKER_USER=101conv_worker\n'
    printf 'POSTGRES_WORKER_PASSWORD=%s\n' "$worker_secret"
    printf 'PORT=5656\n'
  } > "$env_tmp"
  chmod 0600 "$env_tmp"
  mv "$env_tmp" .env
  trap - EXIT
  if ! docker run --rm --volume "$repo_root/data/go-data:/go-data" --entrypoint sh postgres:17-alpine -c 'chown -R 65532:65532 /go-data'; then
    echo 'Could not set Go data ownership. Check the filesystem mount, then set data/go-data to UID/GID 65532:65532 on the Docker host.' >&2
    exit 1
  fi
  echo 'Created private .env and application data storage.'
else
  echo 'Using existing .env without changing credentials or storage paths.'
fi

docker compose up -d --build
echo 'Open http://<docker-host>:5656/ and complete setup.'
echo 'The bootstrap passphrase is ENGINE_ADMIN_TOKEN in .env. Keep .env private and back it up securely.'
