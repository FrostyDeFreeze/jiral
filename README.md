# Jiral

Private file host for one user. Caddy serves uploaded files from disk as is. A
small Go service takes uploads and URL fetches, deletes files and renders WebP
thumbnails and Chatterino/FFZ link cards on request. Only the files are stored,
with no database, index or thumbnail cache.

## Deploy

Needs Go 1.25+, Caddy, `yt-dlp`, `ffmpeg` and `ffprobe` (7.1+ for HEIC). Without
ffmpeg uploads still work, just without thumbnails.

```bash
git clone https://github.com/FrostyDeFreeze/jiral
cd jiral
cp config.example.json config.json
go build -o jiral .
cp examples/jiral.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now jiral
```

Then add `examples/Caddyfile` to the Caddy config with your domain and reload
Caddy. Caddy reads `data/files` itself, so under `/root` it has to run as root
(`systemctl show caddy -p User`). Otherwise move `dataDir` out of `/root`.

Logs: `journalctl -u jiral -f`.

## Config

`config.json` in the working directory. Unknown fields are an error.

```json
{
	"addr": "127.0.0.1:7000",
	"baseUrl": "https://files.example.com",
	"apiToken": "super-secret-token",
	"dataDir": "./data",
	"nameLength": 3,
	"maxFileSizeMB": 256
}
```

| Field           | Default          | Meaning                            |
| --------------- | ---------------- | ---------------------------------- |
| `addr`          | `127.0.0.1:7000` | Go listen address                  |
| `baseUrl`       | `http://<addr>`  | public URL in returned links       |
| `apiToken`      | required         | API token, also signs delete links |
| `dataDir`       | `./data`         | holds `files/` and `tmp/`          |
| `nameLength`    | `3`              | generated name length, 1 to 32     |
| `maxFileSizeMB` | `256`            | upload and fetch limit in MiB      |

Changing `apiToken` breaks every delete link issued so far.

## Paths

| Request                              | Served by | Result                                 |
| ------------------------------------ | --------- | -------------------------------------- |
| `POST /api/upload`, multipart `file` | Go, token | `201` with the JSON below              |
| `POST /api/fetch`, `{"url": "..."}`  | Go, token | yt-dlp download, `201` with the JSON   |
| `DELETE /api/delete/{name}`          | Go, token | `204`                                  |
| `GET /api/delete/{name}/{code}`      | Go        | deletes the file, this is `deleteUrl`  |
| `GET /api/thumbnail/{name}`          | Go        | WebP for images and videos, else `404` |
| `GET /{name}` from Chatterino/FFZ    | Go        | image or `og:` card, see below         |
| `GET /{name}`                        | Caddy     | the file                               |

Chatterino shows images up to 4 MB itself. PNG, JPEG, GIF and WebP up to 4 MB
go to Chatterino/FFZ as is, other images get the WebP thumbnail. This also covers
another site whose `og:image` points here. Videos and other files get an HTML
card with `og:` tags.

Token routes need `Authorization: Bearer <apiToken>`. Errors come back as
`{"error": "..."}`.

```bash
curl -H "Authorization: Bearer $TOKEN" -F file=@pic.png https://files.example.com/api/upload
curl -H "Authorization: Bearer $TOKEN" -d '{"url":"https://clips.twitch.tv/..."}' https://files.example.com/api/fetch
curl -H "Authorization: Bearer $TOKEN" -X DELETE https://files.example.com/api/delete/Ab3.png
```

Upload and fetch return:

```json
{
	"name": "Ab3.png",
	"url": "https://files.example.com/Ab3.png",
	"thumbnailUrl": "https://files.example.com/api/thumbnail/Ab3.png",
	"deleteUrl": "https://files.example.com/api/delete/Ab3.png/xxxxxxxxxx",
	"mime": "image/png",
	"size": 12345
}
```

The extension comes from the uploaded file name, or from the content if the
name has none. To rename a file, `mv` it inside `data/files`; the new name gets
a new delete link.

## Chatterino

```text
Request URL: https://files.example.com/api/upload
Form field: file
Extra headers: Authorization: Bearer super-secret-token
Image link: {url}
Deletion link: {deleteUrl}
```

For ShareX, import `examples/sharex.sxcu` after setting the URL and token.
