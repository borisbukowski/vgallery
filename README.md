# Gallery — a small Piwigo-style photo gallery in Go

Standard library only (no dependencies). Requires Go 1.22+.

## Run

    go run .                                   # http://localhost:8080
    GALLERY_PASSWORD=secret go run . -addr :9000 -data /srv/gallery

If `GALLERY_PASSWORD` is unset, a random admin password is printed at startup.
Log in via **Admin** (top right), create an album, and upload photos.

Build a single binary (templates and CSS are embedded):

    go build -o gallery . && ./gallery

## Bulk import (like Piwigo's folder sync)

    go run . -import ~/Pictures

Every folder becomes an album, nested like the folders; every JPEG/PNG/GIF
becomes a photo. Re-running skips images already in the same album.

## Features

- Nested albums with cover images and recursive photo counts
- Upload (multi-file), edit title/description/tags, delete photos and albums
- Thumbnails (320px square crops) and 1400px viewer size, generated lazily and cached
- EXIF orientation honoured for thumbnails and dimensions
- Tags with a tag cloud, plus full-text search over title/description/tags/filename
- Photo viewer with prev/next and ←/→ keys; download original
- Pagination, responsive layout, automatic dark mode
- Admin login (constant-time compare, HttpOnly SameSite cookie, same-origin check on POSTs)
- Content-hash file names: duplicate uploads in an album are rejected; files are
  only deleted when no photo record uses them

## Data layout

    data/gallery.json   all metadata (albums, photos, tags)
    data/photos/        originals, named by SHA-256
    data/thumbs/        cached renditions

## Not (yet) in this version

Multiple users / permissions, private albums, comments, ratings, EXIF display,
video, moving photos between albums, plugins/themes. The JSON store is fine for
personal galleries; for very large collections swap `Store` for SQLite/Postgres
(its methods are the only thing the handlers touch). Put it behind a TLS
reverse proxy (Caddy/nginx) for public use.
