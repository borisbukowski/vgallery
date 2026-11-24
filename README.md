# vgallery

Dateisystembasierte Go-Webgalerie für JPEG und statische WebP-Dateien mit öffentlicher Ansicht und geschützter Admin-Funktion zum Verschieben unerwünschter Bilder in einen Papierkorb.

Die Galerie einschließlich Originaldateien bleibt ohne Anmeldung erreichbar. Nur administrative Aktionen benötigen eine Admin-Sitzung. Es gibt keine Datenbank und keinen Import.

## Build

Go >= 1.25; beim ersten Build Internetzugang für Module. Die Papierkorb-Funktion verwendet `os.Root.Rename`.

```bash
go get golang.org/x/crypto/bcrypt
go mod tidy
go fmt ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -o vgallery .
```

`main.go` und `gallery.html` gemeinsam aktualisieren. Das HTML wird beim Build eingebettet; nach Template-Änderungen ist ein Neubau erforderlich. Die vollständige neue `main.go` enthält die Anmeldung bereits: Eine zuvor separat angelegte `auth.go` mit der alten Middleware wird nicht mehr benötigt und sollte entfernt werden.

## Admin-Zugang einrichten

Die Anwendung liest beim Start `/etc/vgallery/htpasswd`. Ein anderer Pfad lässt sich mit `-htpasswd` angeben. Die bisherigen Variablen `VGALLERY_USER` und `VGALLERY_PASSWORD_FILE` werden nicht mehr verwendet.

Unterstützt werden ausschließlich bcrypt-Einträge, erzeugt mit Apache `htpasswd -B`. Das Standardformat von `htpasswd` ohne `-B` wird absichtlich nicht unterstützt. Mehrere Benutzer sind möglich; jeder eingetragene Benutzer hat dieselben Admin-Rechte. Fehlende, leere oder ungültige Dateien verhindern den Start. Änderungen erfordern einen Neustart; dieser beendet auch alle bestehenden Sitzungen.

Beispiel für Debian/Ubuntu und einen bereits vorhandenen Dienstbenutzer mit Gruppe `vgallery`:

```bash
sudo apt install apache2-utils
sudo install -d -o root -g vgallery -m 750 /etc/vgallery
sudo sh -c 'umask 027; htpasswd -cB /etc/vgallery/htpasswd boris'
sudo chown root:vgallery /etc/vgallery/htpasswd
sudo chmod 640 /etc/vgallery/htpasswd
sudo install -d -o vgallery -g vgallery -m 750 /var/www/vgallery
```

`-c` nur beim erstmaligen Anlegen verwenden: Es überschreibt eine vorhandene Datei. Weitere Benutzer oder Passwortänderungen:

```bash
sudo htpasswd -B /etc/vgallery/htpasswd boris
sudo systemctl restart vgallery
```

Das Passwort wird interaktiv abgefragt. Apache muss nicht als Webserver laufen; benötigt wird nur dessen `htpasswd`-Werkzeug.

Das Standard-Bildverzeichnis ist `/var/www/vgallery`; mit `-dir` kann es überschrieben werden. Bestehende `-dir`-Argumente in einer systemd-Unit haben weiterhin Vorrang.

### Lokal über HTTP starten

```bash
./vgallery \
    -listen 127.0.0.1:8080 \
    -secure-cookie=false
```

Dann http://127.0.0.1:8080 öffnen. `-secure-cookie=false` nur für lokale HTTP-Nutzung verwenden.

### Hinter einem HTTPS-Reverse-Proxy starten

```bash
./vgallery \
    -cache /var/cache/vgallery \
    -listen 127.0.0.1:8080 \
    -workers 2 \
    -thumb 480 \
    -preview 1920 \
    -title "Meine Fotos"
```

`-secure-cookie` ist standardmäßig `true`. Der Browser muss die Galerie dann über HTTPS erreichen; der Proxy kann intern über Loopback-HTTP mit der Anwendung sprechen. Der Proxy muss den `Authorization`-Header und Cookies weiterreichen.

Weitere Optionen:

```bash
./vgallery -h
```

## Öffentliche Ansicht und Admin-Anmeldung

* Öffentlich bleiben `/`, `/thumb`, `/preview` und `/original`.
* Über „Admin anmelden“ beziehungsweise `/admin/login` öffnet der Browser seine native Benutzername-/Passwortabfrage (HTTP Basic Authentication).
* Nach erfolgreicher Anmeldung setzt die Anwendung ein Sitzungscookie und leitet zur Startseite weiter. Danach erscheint in der Bildvorschau „In den Papierkorb“.
* `POST /trash` erfordert sowohl eine gültige Admin-Sitzung als auch den passenden CSRF-Token. Das Ausblenden der Schaltfläche ist nicht die Zugriffskontrolle; die Prüfung erfolgt im Backend.
* Sitzungen liegen ausschließlich im RAM, gelten acht Stunden und verschwinden bei einem Serverneustart.
* Das Sitzungscookie verwendet `HttpOnly`, `SameSite=Strict` und standardmäßig `Secure`.
* Alle Benutzer aus der htpasswd-Datei sind Admins; es gibt keine Rollen, keine Benutzerverwaltung in der Oberfläche, keinen Logout-Button und keine eingebaute Begrenzung von Anmeldeversuchen.

Basic Authentication verschlüsselt Zugangsdaten nicht. Für Netzwerkzugriff HTTPS verwenden; bei Internetfreigabe zusätzlich Anmeldeversuche am Reverse Proxy begrenzen. Browser können Basic-Auth-Zugangsdaten zwischenspeichern; ein Serverneustart beendet Sitzungen, löscht aber nicht den Zugangsdaten-Cache des Browsers.

Die Admin-Anmeldung schützt nicht die Bilder vor fremdem Lesen. Für eine private Galerie ist eine zusätzliche Zugriffssperre vor der gesamten Anwendung erforderlich.

## Bilder in den Papierkorb verschieben

1. Als Admin anmelden.
2. Ein Bild in der Vorschau öffnen.
3. „In den Papierkorb“ wählen und bestätigen.
4. Nach erfolgreichem Verschieben wird das Bild aus der aktuellen Ansicht entfernt; die Vorschau zeigt das nächste verfügbare Bild.

Das Original wird nicht endgültig gelöscht, sondern innerhalb der Fotowurzel verschoben:

```text
/var/www/vgallery/
  Urlaub/
    Strand.jpg
  Familie/
    Treffen.jpeg
  .trash/
    <UTC-Zeitstempel>-<Zufallskennung>/
      Berge.webp
      original-path.txt
```

`original-path.txt` enthält den vorherigen relativen Pfad, beispielsweise `Urlaub/Berge.webp`. Jeder Vorgang erhält ein eigenes Verzeichnis; vorhandene Papierkorbeinträge werden nicht wiederverwendet.

* `.trash` ist in der Übersicht verborgen und über die Galerie-Routen nicht abrufbar. Ein Symlink als `.trash` wird abgelehnt.
* Das Verschieben benötigt keine Bilddekodierung; auch beschädigte Dateien mit unterstützter Dateiendung können verschoben werden.
* Es gibt keine automatische Leerung, keine endgültige Löschfunktion und keine Wiederherstellungsfunktion in der Oberfläche.
* Zur Wiederherstellung den Pfad in `original-path.txt` prüfen und die Bilddatei manuell dorthin zurückverschieben. Vorhandene Dateien nicht überschreiben; anschließend die Galerie neu laden.
* Verschoben wird per Rename, ohne Copy-and-delete-Fallback. Liegen Quelle und Papierkorb auf unterschiedlichen Dateisystemen, etwa durch eingebundene Unterverzeichnisse, schlägt der Vorgang fehl.
* Alte Thumbnail-/Vorschau-Dateien bleiben gegebenenfalls im Cache. Sie werden nicht automatisch zusammen mit dem Original entfernt.
* Die Anwendung ist weiterhin kein Backup: Papierkorb und Originale liegen innerhalb derselben Fotowurzel.

## Verhalten und Grenzen

* Bestehende Unterverzeichnisse sind Alben; keine Datenbank, kein Import.
* Verzeichnis wird bei jedem Seitenaufruf neu gelesen; Browser neu laden, um neue Dateien zu sehen.
* JPG, JPEG und statisches WebP, unabhängig von Groß-/Kleinschreibung.
* Thumbnail und Vorschau als JPEG, weiße Fläche bei Transparenz.
* Verhältnistreue Verkleinerung, kein Hochskalieren. EXIF-Orientierung über imaging.
* Beide Ableitungen werden beim ersten Bildabruf erzeugt und über temporäre Dateien veröffentlicht.
* Cache-Schlüssel: Quellwurzel, relativer Pfad, Größe, mtime mit Nanosekunden und Zielgrößen.
* Keine Vollinhaltsprüfung: Änderungen mit unveränderter Größe und mtime erfordern Cache-Löschung.
* Cache außerhalb der Fotowurzel; keine automatische Bereinigung alter Varianten.
* Bei gestopptem Dienst kann der dedizierte Cache-Inhalt gelöscht werden.
* Pro Bild höchstens 60 Millionen Pixel standardmäßig; zwei parallele Konvertierungen.
* Das ist keine harte RAM-Grenze: Decoder, Bildkopien und Resize benötigen zusätzlichen Speicher.
* HTTP-Handler laufen parallel; Konvertierungen sind begrenzt, identische Anforderungen gebündelt.
* Versteckte Pfadkomponenten und Symlink-Pfade werden auch bei direktem HTTP-Aufruf abgewiesen, nicht nur in der Übersicht ausgeblendet.
* `os.Root` begrenzt Dateizugriff auf die Fotowurzel. Nur vertrauenswürdige lokale Bestände verwenden; keine gleichzeitigen Manipulationen des Verzeichnisbaums durch nicht vertrauenswürdige lokale Benutzer zulassen.
* Die öffentliche Originalroute veröffentlicht Originaldateien einschließlich eventuell vorhandener EXIF-/GPS-Daten.
* Animierte WebP-Dateien sind nicht unterstützt. Fehlerhafte Dateien bleiben sichtbar, können aber kein Thumbnail erhalten.
* Keine Uploads, Kommentare, Suche oder Piwigo-Kompatibilität.
* Nicht als root betreiben. Standardmäßig nur Loopback; für externen Zugriff HTTPS-Reverse-Proxy verwenden.

## systemd (optional)

Binary nach `/usr/local/bin/vgallery` kopieren und einen separaten Systembenutzer `vgallery` verwenden. Die Service-Datei wird durch diese README nicht automatisch aktualisiert.

Für die neue Version sind folgende Anpassungen nötig:

* Leserechte auf Fotos und Durchquerungsrechte der Elternverzeichnisse vergeben.
* Zum Verschieben benötigt der Dienst Schreib- und Durchquerungsrechte auf den jeweiligen Quellverzeichnissen sowie der Fotowurzel beziehungsweise `.trash`. Reine Leserechte reichen nicht mehr.
* Alte `VGALLERY_USER`- und `VGALLERY_PASSWORD_FILE`-Einstellungen aus dem Dienst entfernen; standardmäßig wird `/etc/vgallery/htpasswd` gelesen.
* Die Passwortdatei muss für den Dienst lesbar sein, darf aber nicht in der öffentlichen Fotowurzel liegen.
* Vorhandene Sandbox-Regeln prüfen: Ein schreibgeschützter Mount oder eine schreibgeschützte Pfadfreigabe für `/var/www/vgallery` verhindert Papierkorbaktionen. `ReadWritePaths` allein ersetzt keine fehlenden Unix-Dateirechte und hebt nicht jede andere Mount-Beschränkung auf.

Beispiel mit bereits vorhandenem Benutzer und Gruppe `vgallery`:

```bash
sudo install -d -o root -g vgallery -m 750 /etc/vgallery
sudo systemctl edit vgallery
```

In das Drop-in eintragen (Pfade gegebenenfalls anpassen):

```ini
[Service]
ReadWritePaths=/var/www/vgallery
```

Das Beispiel setzt voraus, dass der Dienst als Benutzer beziehungsweise mit Gruppe `vgallery` läuft und die vorhandene Service-Konfiguration dazu passt. Für HTTPS-Proxybetrieb bleibt `-secure-cookie` aktiviert; nur bei lokalem HTTP-Test `-secure-cookie=false` zur bestehenden `ExecStart`-Konfiguration ergänzen.

Bei erstmaliger Installation der vorhandenen Service-Datei:

```bash
sudo cp vgallery.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now vgallery
```

Nach Änderungen an einem bereits laufenden Dienst:

```bash
sudo systemctl daemon-reload
sudo systemctl restart vgallery
journalctl -u vgallery -f
```

Die vorhandene Service-Datei sieht laut bisheriger Projektbeschreibung einen Cache über `CacheDirectory` und einen Loopback-Listener vor; diese Einstellungen vor Einsatz prüfen. TLS terminiert am Reverse Proxy, nicht in der Anwendung.

## Prüfung

Die Go-Version wurde in der Erstellungsumgebung mangels Go-Compiler nicht gebaut oder zur Laufzeit getestet. `gallery.html` ist unverändert.

Vor produktivem Einsatz lokal bauen und mit einer Kopie des eigenen Bildbestands prüfen:

* Ohne Anmeldung sind Galerie und Originale erreichbar, die Papierkorb-Schaltfläche jedoch nicht sichtbar.
* `POST /trash` ohne Sitzung wird abgewiesen; mit Sitzung, aber ohne gültigen CSRF-Token ebenfalls.
* Anmeldung über HTTPS funktioniert mit aktiviertem Secure-Cookie; lokale HTTP-Tests verwenden `-secure-cookie=false`.
* Ein bestätigter Vorgang verschiebt nur das gewählte Original und legt dessen ursprünglichen Pfad im Papierkorb ab.
* Direkte Anfragen auf `.trash`, andere versteckte Pfade und Symlink-Pfade werden abgewiesen.
* Fehlende Schreibrechte und dateisystemübergreifende Moves liefern einen Fehler, statt einen erfolgreichen Vorgang vorzutäuschen.
* Nach einem Neustart sind alte Sitzungscookies ungültig.
