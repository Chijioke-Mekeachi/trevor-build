# TrevorBuild backend

TrevorBuild is a Go REST API that accepts a frontend ZIP, builds the frontend, generates a Wails 3 project, and returns a downloadable Android APK or Windows EXE. The backend keeps build configuration, HTTP handlers, build jobs, frontend processing, Wails integration, and file storage in separate packages under `internal/`.

The generated Wails app embeds the uploaded production site from `frontend/dist`. Before reporting an Android build as successful, the backend validates the APK archive, checks for the ARM64 Wails shared library, and confirms that the uploaded `index.html` content is present in that library.

## Requirements

For the Kali Linux Android workflow:

- Go 1.27 or newer
- Wails CLI v3 (`wails3`)
- Task v3
- Node.js and npm; pnpm or Yarn can be selected for uploaded projects
- OpenJDK 21
- Android SDK with API 35 build tools and platform tools
- Android NDK 26.3.11579264
- `adb` is optional and only needed to install to a connected device

The Android package task builds the ARM64 ABI. Windows EXE generation on Linux requires a Wails-supported Windows cross-build toolchain; it is not guaranteed by the backend alone.

## Configure and run locally

Set the Android SDK and NDK paths (adjust them if they are installed elsewhere):

```bash
export ANDROID_HOME=/opt/android-sdk
export ANDROID_SDK_ROOT=/opt/android-sdk
export ANDROID_NDK_HOME=/opt/android-sdk/ndk/26.3.11579264
export PATH="$HOME/go/bin:$PATH"
```

The backend reads these environment variables:

| Variable | Default | Purpose |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | Base URL used in the asynchronous status response |
| `TREVORBUILD_DATA` | `./data` | Isolated job workspaces and downloaded artifacts |
| `BUILD_WORKERS` | `1` | Maximum concurrent builds |
| `WAILS_BIN` | `wails3` | Wails CLI executable |
| `ANDROID_HOME` / `ANDROID_SDK_ROOT` | `/opt/android-sdk` | Android SDK |
| `ANDROID_NDK_HOME` | `$ANDROID_HOME/ndk/26.3.11579264` | Android NDK |
| `WAILS_VITE_PORT` | `9245` | Development server port in generated Wails projects |

`.env.example` lists the common values. The binary does not automatically load `.env` files; export the variables in your shell or service manager.

Build and start the API:

```bash
go test ./...
go build -o trevorbuild .
./trevorbuild
```

The API listens at `http://localhost:8080` by default. Keep the service on a trusted local network: uploaded projects execute package installation and build scripts on the host.

## Submit a frontend build

Upload a `.zip` containing one frontend project. A top-level wrapper directory is supported. If the archive contains more than one project, the API reports an ambiguity instead of choosing one.

```bash
curl -X POST http://localhost:8080/api/v1/builds \
  -F 'file=@vite-test.zip' \
  -F 'icon=@app-icon.png' \
  -F 'config={"appName":"MyApplication","appID":"com.example.myapplication","version":"1.0.0","description":"My application","platform":"android","frontend":{"framework":"auto","packageManager":"npm"},"window":{"title":"MyApplication","width":1200,"height":800,"resizable":true}}'
```

The optional multipart `icon` field accepts PNG, JPG/JPEG, or WebP images up to 10 MiB. Image content and dimensions are validated (maximum 8192 pixels per side and 40 megapixels); unsupported or corrupted images are rejected. The generated Android mipmap resources and Windows application icon use a centered square crop with high-quality resizing. Omitting `icon` keeps the default Wails icon. An optional `icon` filename may also be included in the config JSON; the uploaded multipart file is the actual icon data.

`frontend.framework` may be omitted or set to `auto` for detection. Supported identifiers are:

- `vite`, `react-vite`, `vue-vite`, `svelte-vite`
- `angular`
- `nextjs-static` (requires a Next.js static export)
- `html`, `static`

`frontend.packageManager` may be omitted to detect npm, pnpm, or Yarn from lockfiles. If multiple package-manager lockfiles are present, remove the extras or choose one explicitly. The selected package manager is used exclusively.

ZIP uploads are limited to 100 MiB and icon uploads to 10 MiB; extraction is limited to 500 MiB / 10,000 entries. ZIP traversal paths, symlinks, unsupported entry types, and ambiguous frontend roots are rejected. Framework builds must produce a regular `index.html`; Angular browser output directories and Next.js `_next` assets are checked.

## Check progress and download

The create response includes a job ID and status URL. Poll it until `job.status` is `completed` or a terminal failure/cancellation is reported:

```bash
curl http://localhost:8080/api/v1/builds/JOB_ID
curl -L -o application.apk http://localhost:8080/api/v1/builds/JOB_ID/download
```

Cancel a running job with:

```bash
curl -X DELETE http://localhost:8080/api/v1/builds/JOB_ID
```

Failed job records include the build stage and diagnostic output in `job.error`. Job records are held in memory and are lost when the service restarts; artifacts on disk are not downloadable through the API after restart until persistent job metadata is added.

## API

| Method | Endpoint | Purpose |
|---|---|---|
| `GET` | `/health` | Health check |
| `GET` | `/api/v1/frameworks` | Framework identifiers and default output directories |
| `POST` | `/api/v1/builds` | Multipart upload (`file` ZIP, `config` JSON, optional `icon` image) |
| `GET` | `/api/v1/builds/{id}` | Job status and progress |
| `DELETE` | `/api/v1/builds/{id}` | Cancel a build |
| `GET` | `/api/v1/builds/{id}/download` | Download the APK or EXE |

## Wails integration and development mode

The generated Wails 3 template is inspected at runtime by the build pipeline. Its Go application, build configuration, Android project, and frontend tooling are preserved. The uploaded project source and package dependencies are integrated for development; only generated production output is replaced in `frontend/dist`.

Uploaded Vite configuration is retained as `vite.config.user.*` and wrapped with the Wails Vite plugin. The wrapper sets `server.port` from `WAILS_VITE_PORT` (default `9245`) and enables `strictPort`, without discarding the user's configuration. Angular and Next.js development scripts receive the same port. Production APK/EXE assets are embedded and do not require a development server or localhost service.

The generated project is temporary and is removed after the build completes. This API returns the packaged artifact, not a persistent editable Wails project.

## Troubleshooting

- **Android SDK/NDK not found:** verify the SDK/NDK environment variables and that the ARM64 API-21 compiler exists under `$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/`.
- **Frontend install/build failed:** inspect `job.error`; verify the project lockfile matches its package manifest and that its build script works locally.
- **No production `index.html`:** set `frontend.outputDirectory` to the framework's actual static output directory. Angular output may be nested under `dist/<app>/browser`.
- **Next.js export failed:** configure `output: 'export'` in `next.config.js`; server-side Next.js features are not supported in a packaged static frontend.
- **Windows build failed on Linux:** install and configure the Wails-supported Windows cross-build toolchain for the current host.
- **Device install:** connect an Android device with USB debugging enabled, verify `adb devices`, then use `adb install application.apk`.

## Security and operations warning

This service executes untrusted npm/pnpm/Yarn install hooks, frontend build scripts, and Wails/Gradle builds on the host. Do not expose it directly to the public internet. For a multi-user or public deployment, isolate each build in a disposable, resource-limited VM/container with no host credentials, restricted network egress, CPU/RAM/disk/time limits, rate limiting, and durable job/artifact storage. No API keys, authentication service, or paid external service is required by this implementation.
# trevor-build
