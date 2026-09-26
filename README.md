# nscramble-server

Sync server for the nscramble apps (macOS, iPadOS): one Go binary, one SQLite file.

## Configuration

| Variable            | Default                 | |
|---------------------|-------------------------|-|
| `NSCRAMBLE_API_KEY` | —                       | Required, ≥ 16 characters. Generate with `openssl rand -base64 32`. |
| `NSCRAMBLE_DB`      | `data/nscramble.sqlite` | `/data/nscramble.sqlite` in the image (a volume). |
| `NSCRAMBLE_ADDR`    | `:8080`                 | |

The server speaks plain HTTP; put it behind a TLS-terminating reverse proxy (the apps require `https://`).
Logs are JSON on stdout.

## API

- `GET /health` → `{"ok": true}` (no key needed).
- `POST /sync` with `Authorization: Bearer <key>` and `{"since": <rev>, "changes": [solve…]}` →
  `{"rev": <rev>, "more": <bool>, "changes": [solve…]}`.

A solve is `{id, created_at, date, time_ms, scramble, penalty, updated_at, deleted_at}` (UUID, epoch ms,
`YYYY-MM-DD`, ms, WCA notation, 0/1/2 = none/+2/DNF, epoch ms, epoch ms or null).

Each pushed solve is stored unless the server already has it with an equal or newer `updated_at`
(last write wins); every stored change gets the next revision. The response lists solves with a revision
above `since` in revision order, up to 500 per page; `rev` is the last one's revision and `more` says there
are further pages. Clients send the returned `rev` as `since` next time. At most 500 changes per request.
The client side is `nscramble-app/Packages/Storage/Sources/Storage/Sync.swift`.

## Development

```sh
just test          # go vet + tests
just run           # http://127.0.0.1:8788, key dev-key-0123456789
just docker-build  # image "nscramble-server" for linux/amd64
just docker-run
```

## Deploying

```sh
docker run -d --name nscramble-server --restart unless-stopped \
  -p 127.0.0.1:8080:8080 -e NSCRAMBLE_API_KEY=… -v nscramble-data:/data nscramble-server
```

Back up by copying `nscramble.sqlite` (plus `-wal`/`-shm` if present) from the volume, or with
`sqlite3 nscramble.sqlite ".backup backup.sqlite"`.
