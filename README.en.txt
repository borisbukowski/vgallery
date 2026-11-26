vgallery

A lightweight Go web gallery for JPEG and static WebP files. No database, no import: subdirectories are albums.

- The gallery and the original files are public.
- Only moving images to the trash requires admin login.

Build

Requirement: Go ≥ 1.25 (internet access for modules on the first build).

```bash
go get golang.org/x/crypto/bcrypt
go mod tidy
CGO_ENABLED=0 go build -trimpath -o vgallery .
```

gallery.html is embedded at build time, so rebuild after template changes. An old auth.go is no longer needed and can be deleted.

Admin access

On startup the app reads /etc/vgallery/htpasswd (use -htpasswd for another path).

- bcrypt entries only (htpasswd -B).
- All users have the same admin rights.
- A missing, empty or invalid file prevents startup.
- Changes require a restart, which also ends all sessions.
- VGALLERY_USER and VGALLERY_PASSWORD_FILE are no longer used.

Setup (Debian/Ubuntu, service user and group vgallery already exist):

```bash
sudo apt install apache2-utils
sudo install -d -o root -g vgallery -m 750 /etc/vgallery
sudo sh -c 'umask 027; htpasswd -cB /etc/vgallery/htpasswd boris'
sudo chown root:vgallery /etc/vgallery/htpasswd
sudo chmod 640 /etc/vgallery/htpasswd
sudo install -d -o vgallery -g vgallery -m 750 /var/www/vgallery
```

-c creates the file anew and overwrites an existing one. To add users or change passwords, omit -c and restart the service.

Running

Locally over HTTP:

```bash
./vgallery -listen 127.0.0.1:8080 -secure-cookie=false
```

Behind an HTTPS reverse proxy:

```bash
./vgallery -cache /var/cache/vgallery -listen 127.0.0.1:8080 \
    -workers 2 -thumb 480 -preview 1920 -title "My Photos"
```

| Option | Default | Meaning |
|---|---|---|
| -dir | /var/www/vgallery | Photo directory (write access needed for trash) |
| -listen | 127.0.0.1:8080 | Listen address |
| -cache | user cache dir/vgallery | Cache, outside the photo root |
| -title | Fotogalerie | Gallery title |
| -thumb / -preview | 480 / 1920 | Maximum edge length in px |
| -workers | 2 | Concurrent conversions |
| -max-pixels | 60000000 | Maximum pixels per image |
| -secure-cookie | true | Cookie over HTTPS only; disable only locally |
| -htpasswd | /etc/vgallery/htpasswd | Password file |

The proxy must forward Authorization headers and cookies.

Login and security

- Public: /, /thumb, /preview, /original.
- Log in via "Admin anmelden" (/admin/login) using HTTP Basic Auth. The server then sets a session cookie (HttpOnly, SameSite=Strict, Secure).
- Sessions live in RAM and last 8 hours. A restart ends them.
- POST /trash checks session and CSRF token in the backend.
- There are no roles, no logout and no login rate limit.
- Basic Auth is not encrypted: use HTTPS. For internet exposure, rate-limit login attempts at the proxy.
- Login does not protect images from being read. A private gallery needs an access barrier in front of the whole application.
- Originals are served including EXIF/GPS data.
- Do not run as root.

Trash

1. Log in as admin and open an image in the preview.
2. Choose "In den Papierkorb" (move to trash) and confirm.

The original is moved within the photo root, not deleted:

```text
/var/www/vgallery/
  Urlaub/Strand.jpg
  .trash/
    <UTC-timestamp>-<random-id>/
      Berge.webp
      original-path.txt   # e.g. Urlaub/Berge.webp
```

- .trash is hidden in the gallery and not retrievable.
- There is no automatic emptying, no permanent deletion and no restore in the UI.
- To restore: read the path from original-path.txt, move the file back manually (never overwrite), reload the gallery.
- Moving uses rename. Across filesystem boundaries (e.g. mounted subdirectories) it fails.
- Old thumbnails and previews remain in the cache.
- The trash is not a backup.

Behavior and limits

- Formats: JPG, JPEG, static WebP (case-insensitive). No animated WebP.
- The directory is re-read on every request.
- Thumbnails and previews are JPEG (transparency becomes white), created on first request, never upscaled, EXIF-rotated.
- Cache key: path, size, mtime and target sizes. If an image changes with identical size and mtime, clear the cache.
- Old cache variants are not cleaned up. The cache may be deleted while the service is stopped.
- The pixel limit is not a hard RAM limit.
- Hidden paths and symlinks are rejected even on direct requests. os.Root confines file access to the photo root.
- Use trusted local collections only.
- Broken files stay visible but get no thumbnail.
- Not included: uploads, comments, search, Piwigo compatibility.

systemd (optional)

Copy the binary to /usr/local/bin/vgallery and run the service as system user vgallery. TLS terminates at the reverse proxy.

Required changes for this version:

- Remove VGALLERY_USER and VGALLERY_PASSWORD_FILE from the unit.
- The service needs read and traverse permissions on the photos. For the trash it also needs write permissions on source directories, the photo root and .trash.
- The password file must be readable by the service and must not be inside the photo root.
- Read-only mounts or sandbox rules for /var/www/vgallery prevent trash actions. ReadWritePaths does not replace Unix permissions.

```bash
sudo systemctl edit vgallery
```

```ini
[Service]
ReadWritePaths=/var/www/vgallery
```

First installation:

```bash
sudo cp vgallery.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now vgallery
```

After changes:

```bash
sudo systemctl daemon-reload
sudo systemctl restart vgallery
journalctl -u vgallery -f
```

Testing

This version has not been built or tested (no Go compiler in the authoring environment). Before use, test with a copy of your images:

- Without login, the gallery and originals are reachable and the trash button is absent.
- POST /trash is rejected without a session or without a valid CSRF token.
- Login over HTTPS works with the secure cookie.
- An action moves only the chosen image and creates original-path.txt.
- .trash, hidden paths and symlinks are unreachable via HTTP.
- Missing write permissions and cross-filesystem moves return an error.
- After a restart, old session cookies are invalid.
