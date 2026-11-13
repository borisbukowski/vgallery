# vgallery

Dateisystembasierte, schreibgeschützte Go-Webgalerie für JPEG und statische WebP-Dateien.

## Build

Go >= 1.24; beim ersten Build Internetzugang für Module.

    go mod tidy
    go fmt ./...
    go vet ./...
    CGO_ENABLED=0 go build -trimpath -o vgallery .
    ./vgallery -dir ~/media/good -listen 127.0.0.1:8080

Dann http://127.0.0.1:8080 öffnen.

    ./vgallery -h
    ./vgallery -dir /srv/photos -cache /var/cache/vgallery -listen 0.0.0.0:8080 -workers 2 -thumb 480 -preview 1920 -title "Meine Fotos"

## Verhalten

* Bestehende Unterverzeichnisse sind Alben; keine Datenbank, kein Import.
* Verzeichnis wird bei jedem Seitenaufruf neu gelesen; Browser neu laden, um neue Dateien zu sehen.
* JPG, JPEG und statisches WebP, unabhängig von Groß-/Kleinschreibung.
* Thumbnail und Vorschau als JPEG, weiße Fläche bei Transparenz.
* Verhältnistreue Verkleinerung, kein Hochskalieren. EXIF-Orientierung über imaging.
* Beide Ableitungen werden beim ersten Bildabruf erzeugt und atomar gespeichert.
* Cache-Schlüssel: Quellwurzel, relativer Pfad, Größe, mtime mit Nanosekunden und Zielgrößen.
* Keine Vollinhaltsprüfung: Änderungen mit unveränderter Größe und mtime erfordern Cache-Löschung.
* Cache außerhalb der Fotowurzel; keine automatische Bereinigung alter Varianten.
* Bei gestopptem Dienst kann der dedizierte Cache-Inhalt gelöscht werden.
* Pro Bild höchstens 60 Millionen Pixel standardmäßig; zwei parallele Konvertierungen.
* Das ist keine harte RAM-Grenze: Decoder, Bildkopien und Resize benötigen zusätzlichen Speicher.
* HTTP-Handler laufen parallel; Konvertierungen sind begrenzt, identische Anforderungen gebündelt.
* Ordner/Dateien mit führendem Punkt sowie Symlink-Einträge werden in der Übersicht ausgeblendet.
* os.Root begrenzt Dateizugriff auf die Fotowurzel. Symlinks innerhalb dieser Wurzel können bei direkter URL adressiert werden; Dotfiles werden nur ausgeblendet, nicht als Berechtigungsgrenze behandelt.
* Originalroute veröffentlicht Originaldateien einschließlich eventuell vorhandener EXIF-/GPS-Daten.
* Animierte WebP-Dateien sind nicht unterstützt. Fehlerhafte Dateien bleiben sichtbar, können aber kein Thumbnail erhalten.
* Kein Login, keine Uploads, Benutzerrechte, Kommentare, Suche oder Piwigo-Kompatibilität.
* Nur vertrauenswürdige lokale Bildbestände verwenden. Nicht als root betreiben.
* Standardmäßig nur Loopback. Für externen Zugriff Reverse Proxy mit HTTPS und Authentifizierung nutzen.

## Beispielstruktur

    /srv/photos/
      Urlaub/
        Strand.jpg
        Berge.webp
      Familie/
        Treffen.jpeg

## systemd (optional)

Binary nach /usr/local/bin/vgallery kopieren. Separaten Systembenutzer vgallery anlegen,
Leserechte auf Fotos und Durchquerungsrechte der Elternverzeichnisse vergeben.
Die Service-Datei setzt /srv/photos voraus; gegebenenfalls anpassen.

    sudo cp vgallery.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable --now vgallery
    journalctl -u vgallery -f

Der Dienst legt seinen Cache via CacheDirectory an und lauscht nur auf Loopback.
Kein TLS oder Login eingebaut.

## Prüfung

Das Projekt wurde in der Erstellungsumgebung nicht mit einem Go-Compiler ausgeführt.
Vor produktivem Einsatz lokal bauen und mit dem eigenen Bildbestand testen.
