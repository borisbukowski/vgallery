# VGallery

A simple Go image gallery that groups photos by **year and month of capture** and serves them over HTTP. It uses EXIF capture time when available and falls back to file modification time if EXIF data is missing.[cite:25][cite:103]

## Features

- Groups images by **year and month** using capture time when possible.[cite:25][cite:103]
- Shows a **preview image** for each month on the index page by using the first image in that month group.[cite:120]
- Serves each month on its own page at `/month/YYYY-MM` instead of putting everything on one page.[cite:103]
- Serves original image files from `/imgs/...` with configurable cache headers.[cite:63]

## Requirements

- Go installed locally.
- A folder containing images such as `.jpg`, `.jpeg`, `.png`, `.gif`, or `.webp`.
- A Go module initialized in the project directory, because dependency management for libraries is module-based.[cite:43][cite:35]

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

This creates a `go.mod` file for the project.[cite:43]

### 4. Add the EXIF dependency

Use the `cozy` module path, because that is the module path declared by the package itself.[cite:25][cite:46]

```bash
go get github.com/cozy/goexif2/exif
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

- `-dir` — directory containing images, supports paths starting with `~/`.
- `-listen` — address to bind, for example `:8080` or `127.0.0.1:9000`.
- `-title` — title shown in the HTML pages.
- `-thumb-height` — thumbnail height in pixels on month pages.
- `-cache-max-age` — cache lifetime in seconds for served images.
- `-cache-immutable` — whether to append `immutable` to the `Cache-Control` header.[cite:63]

Example:

```bash
./vgallery \
  -dir ~/media/good \
  -listen 127.0.0.1:9000 \
  -title "My Photos" \
  -thumb-height 220
```

## Routes

The server exposes these main paths:

- `/` — index page listing months with a preview image.
- `/month/YYYY-MM` — page for a single month.
- `/imgs/FILENAME` — original image file.[cite:63]

Example month URL:

```text
http://localhost:8080/month/2026-04
```

## How sorting works

Images are sorted newest first using the capture timestamp when EXIF is available, and Go time formatting like `2006-01` is used to produce sortable year-month keys.[cite:25][cite:103][cite:104]

## Troubleshooting

### `go: go.mod file not found`

Initialize the project as a module first:

```bash
go mod init vgallery
```

That is required before adding library dependencies with `go get`.[cite:43][cite:35]

### `module declares its path as github.com/cozy/goexif2`

Use this import and dependency path:

```go
import "github.com/cozy/goexif2/exif"
```

The `mholt` path does not match the declared module path, which causes Go module resolution to fail.[cite:25][cite:44][cite:46]

### New images do not appear

This version scans the image directory at startup, so restarting the program reloads the gallery data.

```bash
Ctrl+C
./vgallery -dir ~/media/good -listen :8080 -title "Gallery"
```

## Notes

- JPEG files are the most likely to contain usable EXIF capture time; other image formats often fall back to file modification time.[cite:25]
- Year-month page keys are generated with Go's time layout formatting, which uses the reference date `2006-01-02` style layouts rather than `YYYY-MM-DD` tokens.[cite:104][cite:114]
