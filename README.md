# Jiral

Minimal Go file uploader. Jiral accepts authenticated uploads, stores files on
disk, returns public links, serves files directly and generates local WebP
thumbnails

## Scope

- Private API for upload, rename and delete
- Public direct file, thumbnail and delete-link routes
- Local JSON index and filesystem storage
- Generated file names; original upload names are not stored or logged
- No gallery, listing UI, user management or database

## Requirements

- Go 1.25 or newer
- `ffmpeg` available on `PATH` for preview generation

## Configuration

The server reads `config.json` from the current working directory

```json
{
	"addr": "127.0.0.1:7000",
	"baseUrl": "https://files.example.com",
	"apiToken": "super-secret-token",
	"dataDir": "./data",
	"fileCodeLength": 3,
	"maxFileSize": 268435456,
	"previewMaxPixels": 51984,
	"previewMaxSide": 300,
	"previewFPS": 15,
	"previewFrames": 100
}
```

`apiToken` is required. `baseUrl` is normalized without a trailing slash and
should be the public URL when the service runs behind Caddy. `addr` can stay on
`127.0.0.1:7000` so the service only listens locally

## Deploy

The example service expects the project in `/root/projects/jiral`, with the
compiled binary named `jiral` and `config.json` in the same directory

```bash
git clone https://github.com/FrostyDeFreeze/jiral
cd jiral
cp config.example.json config.json
go build -o jiral .
chmod +x ./jiral
cp examples/jiral.service /etc/systemd/system/jiral.service
systemctl daemon-reload
systemctl enable --now jiral
systemctl status jiral
```

Logs are available through systemd:

```bash
journalctl -u jiral -f
```

## Caddy Reverse Proxy

Example Caddy site:

```caddy
files.example.com {
	reverse_proxy 127.0.0.1:7000
}
```

With this setup, keep `addr` as `127.0.0.1:7000` and set `baseUrl` to
`https://files.example.com`. Do not add Caddy `file_server` in this mode:
the Go server needs to see public requests so it can apply delete links,
index-backed file lookup and thumbnail responses for preview crawlers

## Private API

All private routes require:

```http
Authorization: Bearer <API_TOKEN>
```

Upload one file from multipart field `file`:

```bash
curl -H "Authorization: Bearer super-secret-token" -F "file=@example.png" http://127.0.0.1:7000/api/files
```

The upload response includes `url`, `thumbnailUrl` and `deleteUrl`

Rename a file while preserving its extension:

```bash
curl -X PATCH -H "Authorization: Bearer super-secret-token" -H "Content-Type: application/json" -d '{"name":"forsen"}' http://127.0.0.1:7000/api/files/jir.png
```

Delete a file:

```bash
curl -X DELETE -H "Authorization: Bearer super-secret-token" http://127.0.0.1:7000/api/files/forsen.png
```

## Public Routes

- `GET /{name}` serves the original file directly
- `GET /{name}?delete=<code>` deletes the file, thumbnail and index entry when
  `<code>` matches the file's delete code
- `GET /t/{name}` serves the generated WebP thumbnail if available

Unknown content is accepted as `application/octet-stream` with a `.bin`
extension

## Chatterino

Use these image uploader settings:

```text
Request URL: https://files.example.com/api/files
Form field: file
Extra headers: Authorization: Bearer super-secret-token
Image link: {url}
Deletion link: {deleteUrl}
```

## ShareX

`examples/sharex.sxcu` is a ShareX custom uploader. Replace the request URL and
API token before importing it

## Storage

```text
data/
  index.json
  files/
  previews/
  tmp/
```

Keep `data/` private when using the recommended Go-only serving mode. Public
access should go through the Go server

## Project Layout

```text
main.go             process entrypoint
config.go           JSON config loading and defaults
server.go           server construction, routing entry, auth, paths, logging
handlers_api.go     authenticated upload, rename and delete routes
handlers_public.go  public direct file, thumbnail and delete-link routes
storage.go          index metadata and delete-link backfill
upload.go           multipart upload, tmp write, final publish
media.go            ffmpeg sanitize and WebP thumbnail generation
mime.go             content MIME detection
stable_write.go     stable cross-platform index writes
util.go             small shared helpers
```
