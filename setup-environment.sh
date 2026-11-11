#!/bin/sh

if [ -n "${BASH_SOURCE:-}" ]; then
    if [ "$BASH_SOURCE" = "$0" ]; then
        echo "Source this file from the project root: . ./setup-environment.sh" >&2
        exit 1
    fi
else
    case "$0" in
        *setup-environment.sh)
            echo "Source this file from the project root: . ./setup-environment.sh" >&2
            exit 1
            ;;
    esac
fi

if [ ! -f go.mod ]; then
    echo "Run this script from the TrevorBuild project root." >&2
    return 1
fi

export LISTEN_ADDR=${LISTEN_ADDR:-:8080}
export PUBLIC_BASE_URL=${PUBLIC_BASE_URL:-http://localhost:8080}
export TREVORBUILD_DATA=${TREVORBUILD_DATA:-./data}
export BUILD_WORKERS=${BUILD_WORKERS:-1}
export WAILS_BIN=${WAILS_BIN:-wails3}
export ANDROID_HOME=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-/opt/android-sdk}}
export ANDROID_SDK_ROOT=${ANDROID_SDK_ROOT:-$ANDROID_HOME}
export ANDROID_NDK_HOME=${ANDROID_NDK_HOME:-$ANDROID_HOME/ndk/26.3.11579264}
export WAILS_VITE_PORT=${WAILS_VITE_PORT:-9245}
export PATH="$HOME/go/bin:$PATH"

mkdir -p "$TREVORBUILD_DATA/jobs" "$TREVORBUILD_DATA/artifacts"

missing=""
for tool in go node npm java task; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        missing="${missing}${missing:+, }$tool"
    fi
done
if ! command -v "$WAILS_BIN" >/dev/null 2>&1; then
    missing="${missing}${missing:+, }$WAILS_BIN"
fi

if [ -n "$missing" ]; then
    echo "Missing required tools: $missing" >&2
    echo "Install the local toolchain or use ./create-environment.sh to build the Docker image." >&2
    return 1
fi
if [ ! -x "$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android21-clang" ]; then
    echo "Android NDK compiler not found under $ANDROID_NDK_HOME." >&2
    return 1
fi
if [ ! -d "$ANDROID_HOME" ]; then
    echo "Android SDK not found at $ANDROID_HOME." >&2
    return 1
fi

printf 'TrevorBuild environment is configured (API: %s, data: %s).\n' "$LISTEN_ADDR" "$TREVORBUILD_DATA"
