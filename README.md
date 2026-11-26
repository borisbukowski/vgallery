# vgallery

Schlanke Go-Webgalerie für JPEG und statische WebP-Dateien. Ohne Datenbank, ohne Import: Unterverzeichnisse sind Alben.

- Galerie und Originale sind öffentlich.
- Nur das Verschieben in den Papierkorb erfordert eine Admin-Anmeldung.

## Build

Voraussetzung: Go ≥ 1.25 (beim ersten Build Internet für Module).

```bash
go get golang.org/x/crypto/bcrypt
go mod tidy
CGO_ENABLED=0 go build -trimpath -o vgallery .
```

`gallery.html` wird eingebettet. Nach Template-Änderungen neu bauen. Eine alte `auth.go` wird nicht mehr benötigt und kann gelöscht werden.

## Admin-Zugang

Beim Start wird `/etc/vgallery/htpasswd` gelesen (anderer Pfad: `-htpasswd`).

- Nur bcrypt-Einträge (`htpasswd -B`).
- Alle Benutzer haben dieselben Admin-Rechte.
- Eine fehlende, leere oder ungültige Datei verhindert den Start.
- Änderungen erfordern einen Neustart und beenden alle Sitzungen.
- `VGALLERY_USER` und `VGALLERY_PASSWORD_FILE` entfallen.

Einrichtung (Debian/Ubuntu, Dienstbenutzer und Gruppe `vgallery` existieren):

```bash
sudo apt install apache2-utils
sudo install -d -o root -g vgallery -m 750 /etc/vgallery
sudo sh -c 'umask 027; htpasswd -cB /etc/vgallery/htpasswd boris'
sudo chown root:vgallery /etc/vgallery/htpasswd
sudo chmod 640 /etc/vgallery/htpasswd
sudo install -d -o vgallery -g vgallery -m 750 /var/www/vgallery
```

`-c` legt die Datei neu an und überschreibt sie. Für weitere Benutzer oder neue Passwörter `-c` weglassen und den Dienst neu starten.

## Starten

Lokal über HTTP:

```bash
./vgallery -listen 127.0.0.1:8080 -secure-cookie=false
```

Hinter einem HTTPS-Reverse-Proxy:

```bash
./vgallery -cache /var/cache/vgallery -listen 127.0.0.1:8080 \
    -workers 2 -thumb 480 -preview 1920 -title "Meine Fotos"
```

| Option | Standard | Bedeutung |
|---|---|---|
| `-dir` | `/var/www/vgallery` | Fotoverzeichnis (Schreibrechte für Papierkorb nötig) |
| `-listen` | `127.0.0.1:8080` | Listen-Adresse |
| `-cache` | User-Cache/vgallery | Cache, außerhalb der Fotowurzel |
| `-title` | `Fotogalerie` | Titel |
| `-thumb` / `-preview` | 480 / 1920 | Maximale Kantenlänge in px |
| `-workers` | 2 | Parallele Konvertierungen |
| `-max-pixels` | 60000000 | Maximale Pixel pro Bild |
| `-secure-cookie` | `true` | Cookie nur über HTTPS; nur lokal abschalten |
| `-htpasswd` | `/etc/vgallery/htpasswd` | Passwortdatei |

Der Proxy muss `Authorization` und Cookies durchreichen.

## Anmeldung und Sicherheit

- Öffentlich: `/`, `/thumb`, `/preview`, `/original`.
- Anmeldung über „Admin anmelden“ (`/admin/login`) per HTTP Basic Auth. Danach setzt der Server ein Sitzungscookie (`HttpOnly`, `SameSite=Strict`, `Secure`).
- Sitzungen liegen im RAM und gelten 8 Stunden. Ein Neustart beendet sie.
- `POST /trash` prüft im Backend Sitzung und CSRF-Token.
- Es gibt keine Rollen, keinen Logout und kein Login-Limit.
- Basic Auth ist unverschlüsselt: HTTPS verwenden. Bei Internetzugang Anmeldeversuche am Proxy begrenzen.
- Die Anmeldung schützt Bilder nicht vor Lesezugriff. Eine private Galerie braucht eine Zugangssperre vor der ganzen Anwendung.
- Originale werden samt EXIF-/GPS-Daten ausgeliefert.
- Nicht als root betreiben.

## Papierkorb

1. Als Admin anmelden und ein Bild in der Vorschau öffnen.
2. „In den Papierkorb“ wählen und bestätigen.

Das Original wird innerhalb der Fotowurzel verschoben, nicht gelöscht:

```text
/var/www/vgallery/
  Urlaub/Strand.jpg
  .trash/
    <UTC-Zeitstempel>-<Zufallskennung>/
      Berge.webp
      original-path.txt   # z. B. Urlaub/Berge.webp
```

- `.trash` ist in der Galerie verborgen und nicht abrufbar.
- Es gibt keine automatische Leerung, kein endgültiges Löschen und keine Wiederherstellung in der Oberfläche.
- Wiederherstellen: Pfad aus `original-path.txt` lesen, Datei manuell zurückverschieben (nichts überschreiben), Galerie neu laden.
- Verschoben wird per Rename. Über Dateisystemgrenzen (z. B. gemountete Unterverzeichnisse) schlägt das fehl.
- Alte Thumbnails und Vorschauen bleiben im Cache.
- Der Papierkorb ersetzt kein Backup.

## Verhalten und Grenzen

- Formate: JPG, JPEG, statisches WebP (Groß-/Kleinschreibung egal). Keine animierten WebP.
- Das Verzeichnis wird bei jedem Aufruf neu gelesen.
- Thumbnail und Vorschau sind JPEG (Transparenz wird weiß), werden beim ersten Abruf erzeugt, nie hochskaliert und nach EXIF gedreht.
- Cache-Schlüssel: Pfad, Größe, mtime und Zielgrößen. Ändert sich ein Bild bei gleicher Größe und mtime, den Cache leeren.
- Alte Cache-Varianten werden nicht bereinigt. Bei gestopptem Dienst darf der Cache gelöscht werden.
- Das Pixellimit ist keine harte RAM-Grenze.
- Versteckte Pfade und Symlinks werden auch bei direktem Aufruf abgewiesen. `os.Root` beschränkt den Dateizugriff auf die Fotowurzel.
- Nur vertrauenswürdige lokale Bestände verwenden.
- Defekte Dateien bleiben sichtbar, erhalten aber kein Thumbnail.
- Nicht enthalten: Uploads, Kommentare, Suche, Piwigo-Kompatibilität.

## systemd (optional)

Binary nach `/usr/local/bin/vgallery` kopieren, Dienst unter dem Systembenutzer `vgallery` laufen lassen. TLS terminiert am Reverse Proxy.

Anpassungen für diese Version:

- `VGALLERY_USER` und `VGALLERY_PASSWORD_FILE` aus der Unit entfernen.
- Der Dienst braucht Lese- und Durchquerungsrechte auf die Fotos. Für den Papierkorb braucht er zusätzlich Schreibrechte auf Quellverzeichnisse, Fotowurzel und `.trash`.
- Die Passwortdatei muss für den Dienst lesbar sein und darf nicht in der Fotowurzel liegen.
- Schreibgeschützte Mounts oder Sandbox-Regeln für `/var/www/vgallery` verhindern den Papierkorb. `ReadWritePaths` ersetzt keine Unix-Rechte.

```bash
sudo systemctl edit vgallery
```

```ini
[Service]
ReadWritePaths=/var/www/vgallery
```

Erstinstallation:

```bash
sudo cp vgallery.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now vgallery
```

Nach Änderungen:

```bash
sudo systemctl daemon-reload
sudo systemctl restart vgallery
journalctl -u vgallery -f
```

## Testen

Die Version wurde nicht gebaut oder getestet (kein Go-Compiler in der Erstellungsumgebung). Vor dem Einsatz mit einer Kopie der Bilder prüfen:

- Ohne Anmeldung sind Galerie und Originale erreichbar, die Papierkorb-Schaltfläche fehlt.
- `POST /trash` wird ohne Sitzung oder ohne gültigen CSRF-Token abgewiesen.
- Die Anmeldung über HTTPS funktioniert mit Secure-Cookie.
- Ein Vorgang verschiebt nur das gewählte Bild und legt `original-path.txt` an.
- `.trash`, versteckte Pfade und Symlinks sind per HTTP nicht erreichbar.
- Fehlende Schreibrechte und Moves über Dateisystemgrenzen liefern einen Fehler.
- Nach einem Neustart sind alte Sitzungscookies ungültig.
