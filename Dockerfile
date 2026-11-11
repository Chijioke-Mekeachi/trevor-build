FROM eclipse-temurin:21-jdk AS java
FROM node:22-bookworm-slim AS node

FROM golang:1.27-bookworm

ARG ANDROID_COMMAND_LINE_TOOLS_VERSION=11076708

ENV JAVA_HOME=/opt/java/openjdk \
    ANDROID_HOME=/opt/android-sdk \
    ANDROID_SDK_ROOT=/opt/android-sdk \
    ANDROID_NDK_HOME=/opt/android-sdk/ndk/26.3.11579264 \
    WAILS_BIN=/usr/local/bin/wails3 \
    LISTEN_ADDR=:8080 \
    PUBLIC_BASE_URL=http://localhost:8080 \
    TREVORBUILD_DATA=/data \
    BUILD_WORKERS=1 \
    WAILS_VITE_PORT=9245 \
    GOPATH=/home/trevorbuild/go \
    GOCACHE=/home/trevorbuild/.cache/go-build \
    GOBIN=/usr/local/bin \
    PATH=/opt/java/openjdk/bin:/opt/android-sdk/cmdline-tools/latest/bin:/opt/android-sdk/platform-tools:/usr/local/go/bin:/usr/local/bin:${PATH}

COPY --from=java /opt/java/openjdk /opt/java/openjdk
COPY --from=node /usr/local/ /usr/local/

RUN apt-get update && apt-get install -y --no-install-recommends \
        bash \
        build-essential \
        ca-certificates \
        curl \
        git \
        lib32stdc++6 \
        lib32z1 \
        libgtk-3-dev \
        libwebkit2gtk-4.1-dev \
        pkg-config \
        unzip \
        zip \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p "${ANDROID_HOME}/cmdline-tools" \
    && curl -fsSL "https://dl.google.com/android/repository/commandlinetools-linux-${ANDROID_COMMAND_LINE_TOOLS_VERSION}_latest.zip" -o /tmp/android-command-line-tools.zip \
    && unzip -q /tmp/android-command-line-tools.zip -d /tmp/android-command-line-tools \
    && mv /tmp/android-command-line-tools/cmdline-tools "${ANDROID_HOME}/cmdline-tools/latest" \
    && rm -rf /tmp/android-command-line-tools /tmp/android-command-line-tools.zip \
    && yes | sdkmanager --licenses >/dev/null \
    && sdkmanager \
        "platform-tools" \
        "platforms;android-35" \
        "build-tools;35.0.0" \
        "ndk;26.3.11579264"

RUN go install github.com/go-task/task/v3/cmd/task@latest \
    && go install github.com/wailsapp/wails/v3/cmd/wails3@latest

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /usr/local/bin/trevorbuild .

RUN useradd --create-home --uid 10001 trevorbuild \
    && mkdir -p "${TREVORBUILD_DATA}" \
    && chown -R trevorbuild:trevorbuild "${TREVORBUILD_DATA}" /home/trevorbuild

USER trevorbuild

EXPOSE 8080
VOLUME ["/data"]

CMD ["/usr/local/bin/trevorbuild"]
