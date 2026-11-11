#!/bin/sh
set -eu

cd "$(dirname "$0")"

if ! command -v docker >/dev/null 2>&1; then
    echo "Docker is required to create the TrevorBuild environment." >&2
    exit 1
fi

image_name=${IMAGE_NAME:-trevorbuild-backend:local}
docker build --platform linux/amd64 --file Dockerfile --tag "$image_name" .

printf 'Created Docker image: %s\n' "$image_name"
printf 'Run it with: docker run --rm -p 8080:8080 -v trevorbuild-data:/data %s\n' "$image_name"
