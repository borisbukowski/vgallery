# VGallery

A simple Go image gallery that groups photos by **year and month of capture** and serves them over HTTP. It uses EXIF capture time when available and falls back to file modification time when EXIF data is missing.

## Features

- Groups images by **year and month** using capture time when possible.
- Sorts images newest first.
- Shows a **preview image** for each month on the index page.
- Serves a separate page for each month at `/month/YYYY-MM`.
- Falls back to file modification time for formats without usable EXIF metadata.

## Requirements

- Go installed locally.
- A folder containing images such as `.jpg`, `.jpeg`, `.png`, `.gif`, or `.webp`.
- A Go module initialized in the project directory.

## Project layout

Typical files in the project directory:

```text
vgallery/
├── gallery.go
├── go.mod
└── go.sum
```

## Installation

### 1. Create or enter the project directory

```bash
mkdir -p ~/d/vgallery
cd ~/d/vgallery
```

### 2. Save the source code

Save the Go source as `gallery.go` in that directory.

### 3. Initialize the Go module

```bash
go mod init vgallery
```

### 4. Add the EXIF dependency

Use the same module path as the current source code:

```bash
go get github.com/xor-gate/goexif2/exif
go mod tidy
```

## Running the gallery

Run the server with the image directory you want to expose:

```bash
go run gallery.go -dir ~/media/good -listen :8080 -title "Gallery"
```

Then open:

```text
http://localhost:8080
```

## Build a binary

To build a standalone executable:

```bash
go build -o vgallery gallery.go
```

Then run it:

```bash
./vgallery -dir ~/media/good -listen :8080 -title "Gallery"
```

## Command-line flags

The program supports these flags:

- `-dir` — directory containing images, including paths starting with `~/`.
- `-listen` — address to bind, for example `:8080` or `127.0.0.1:9000`.
- `-title` — title shown in the HTML pages.
- `-thumb-height` — thumbnail height in pixels on month pages.

Example:

```bash
./vgallery   -dir ~/media/good   -listen 127.0.0.1:9000   -title "My Photos"   -thumb-height 220
```

## Routes

The server exposes these main paths:

- `/` — index page listing months with a preview image.
- `/month/YYYY-MM` — page for a single month.

Example month URL:

```text
http://localhost:8080/month/2026-04
```

## How sorting works

Images are sorted newest first using the capture timestamp when EXIF is available. Month grouping keys are generated with Go time formatting such as `2006-01`, which keeps year-month values sortable.

## Troubleshooting

### `go: go.mod file not found`

Initialize the project as a module first:

```bash
go mod init vgallery
```

Then add the dependency and tidy the module:

```bash
go get github.com/xor-gate/goexif2/exif
go mod tidy
```

### EXIF import path mismatch

Use this import path in the source code:

```go
import "github.com/xor-gate/goexif2/exif"
```

If `go get` or `go build` complains about a different module path, update the code and module dependencies so they all reference the same package path.

### New images do not appear

This version scans the image directory at startup, so restarting the program reloads the gallery data.

```bash
Ctrl+C
./vgallery -dir ~/media/good -listen :8080 -title "Gallery"
```

## Notes

- JPEG images are the most likely to contain usable EXIF capture time.
- Other formats often fall back to file modification time.
- Go time layouts use the reference date `2006-01-02`, not `YYYY-MM-DD` tokens.
