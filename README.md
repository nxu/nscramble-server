# nscramble-server

Sync server for the nscramble apps (macOS, iPadOS): one Go binary, one SQLite file.

## Configuration

| Variable            | Default                 | |
|---------------------|-------------------------|-|
| `NSCRAMBLE_API_KEY` | —                       | Required, ≥ 16 characters. Generate with `openssl rand -base64 32`. |
| `NSCRAMBLE_DB`      | `data/nscramble.sqlite` | `/data/nscramble.sqlite` in the image (a volume). |
| `NSCRAMBLE_ADDR`    | `:8080`                 | |
| `NSCRAMBLE_TZ`      | `UTC`                   | IANA time zone that decides which day is "today" for `/stats`, e.g. `Europe/Budapest`. |
| `NSCRAMBLE_BEHIND_PROXY` | unset              | `true` to take client IPs from `X-Forwarded-For` for rate limiting. Set it behind a reverse proxy (otherwise every client shares the proxy's address); never without one, or clients could pick their own address. |

The server speaks plain HTTP; put it behind a TLS-terminating reverse proxy (the apps require `https://`).
Logs are JSON on stdout.

## API

- `GET /health` → `{"ok": true}` (no key needed).
- `GET /stats` → public statistics for a website (no key; CORS `*`; cached in memory for 60 s):

  ```json
  {
    "generated_at": "2026-09-27T10:00:00Z",
    "recent_session": { "date": "2026-09-26", "solves": 42, "average_ms": 17370, "median_ms": 17200, "std_dev_ms": 2310 },
    "average_history": [ { "date": "2026-08-30", "average_ms": 18120 }, … ],
    "solve_count_history": [ { "date": "2026-08-30", "solves": 35 }, … ]
  }
  ```

  The recent session is the latest date with solves **before today** (in `NSCRAMBLE_TZ`), since today's
  session may still be in progress; it's `null` if there is none. The histories cover the last 30 dates with
  solves, including today, oldest first. Times are milliseconds: the average drops the fastest and slowest 5% (at least one each,
  DNF counting as slowest) and is rounded to 10 ms, like the app; the median counts DNFs as slowest; the
  standard deviation (population) uses the non-DNF times. `null` means a DNF result or too few solves.
  Dates are the solving device's local calendar days. Deleted solves are ignored.
- Failed API-key attempts are rate limited per client IP: after 5 within 15 minutes, that client gets
  `429 Too Many Requests` (with `Retry-After`) for 15 minutes, even with the right key. A successful
  request clears the count. Limits are in memory and reset on restart.
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

## CI and releases

GitHub Actions (`.github/workflows/`): every push and pull request runs `gofmt`, `go vet` and the tests.
Pushing a version tag (`1.2.3` or `v1.2.3`) runs the tests and publishes a multi-arch image
(linux/amd64, linux/arm64) to `ghcr.io/<owner>/nscramble-server` tagged `1.2.3`, `1.2`, `1` and `latest`.
The version is built into the binary and logged at startup.

## Deploying

```sh
docker run -d --name nscramble-server --restart unless-stopped \
  -p 127.0.0.1:8080:8080 -e NSCRAMBLE_API_KEY=… -e NSCRAMBLE_TZ=Europe/Budapest \
  -v nscramble-data:/data ghcr.io/nxu/nscramble-server:latest
```

Back up by copying `nscramble.sqlite` (plus `-wal`/`-shm` if present) from the volume, or with
`sqlite3 nscramble.sqlite ".backup backup.sqlite"`.
