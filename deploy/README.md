# Running the M1 container

M1 serves one movie to one viewer on a **trusted private network** over plain
HTTP. Plain HTTP has no transport encryption: don't forward the port to the
internet. Remote access with HTTPS is later work (spec §6.6).

## 1. Folders and permissions

The container runs as `PUID:PGID` (default `1000:1000`), never as root, and
never changes ownership of your files.

| Folder | Mounted at | Needs |
|---|---|---|
| `CONFIG_DIR` (default `./config`) | `/config` | **owned by** `PUID:PGID`, mode 0700. Holds `m1.json`, `lisimflix.db` and `backups/` |
| `MEDIA_DIR` | `/media` (read-only) | **readable** by `PUID:PGID`; it is never written |

```sh
mkdir -p config && sudo chown 1000:1000 config && chmod 700 config
```

`m1.json`, the database and every snapshot are created 0600. They hold the
password hash and the CSRF key: keep `CONFIG_DIR` private, and treat copies of
`backups/` as sensitive.

## 2. Build the image

```sh
make image          # tags lisimflix:m1
```

## 3. Provision the viewer and movie (`init`)

Generate the probe record for the movie (see `testdata/media/README.md` for
the M1 test movie), then run `init` once. The password is read from a private
file through stdin, never typed on the command line, so it stays out of shell
history. Put the password on two lines (the same password twice) in a file only you
can read, for example `umask 077 && nano pwfile`, then:

```sh
MEDIA_DIR=/path/to/media docker compose -f deploy/compose.m1.yml run --rm -T lisimflix \
  init --config /config/m1.json --origin http://192.168.1.20:8420 \
       --media-root /media --movie "Movies/tears_of_steel_1080p.mp4" \
       --probe-json /media/tears_of_steel_1080p.probe.json --username viewer < pwfile
rm pwfile
```

`--origin` is the address browsers use, with the **host** port: compose
publishes `LISIMFLIX_PORT` (default 8420) to the app's fixed 8420 inside the
container, so keep `init`'s `--listen` at its default. `init` refuses to
overwrite an existing config. Passwords are 12–1024 bytes.

**IPv4 only.** The compose file publishes the port on IPv4 only. With Docker's
default publishing, IPv6 clients reach the app through Docker's userland proxy,
so the app sees the Docker bridge gateway instead of the client. Every IPv6
client would then share one sign-in rate limit and one password-check slot. If
you need IPv6, run with `network_mode: host` instead (then remove `ports:`; the
app listens on `--listen`, port 8420 by default). Clients that connect by name
still work: they fall back to IPv4.

## 4. Run

```sh
MEDIA_DIR=/path/to/media TZ=Australia/Brisbane docker compose -f deploy/compose.m1.yml up -d
```

- **Health:** the image's `HEALTHCHECK` runs `lisimflix healthcheck`.
  `/health/ready` is 503 until the database is migrated and the movie is present
  and unchanged since `init`.
- **Backups:** a snapshot is taken before any migration of an existing
  database, and nightly at 03:00 in `TZ` (UTC if unset); 7 nightly ones are
  kept. For one now: `docker compose -f deploy/compose.m1.yml exec lisimflix lisimflix backup-now`.
  Copy `backups/` to a second location yourself (spec §11.3).
- **Stop:** `docker compose down` gives the 15-second grace period; open
  streams are cut and the database is closed cleanly.
- **Logs:** the server writes JSON lines to stderr (`docker compose -f deploy/compose.m1.yml logs`):
  sign-in results, session ends, playback starts and ends, source errors, and requests that
  ended in an error, each with a `request_id` that matches the response's `X-Request-Id`.
  Passwords, cookies, CSRF tokens, content grants and query strings are never logged.
- **Newer database:** if the log says the database is newer than this version,
  start the matching image. M1 has no restore command yet (admin restore is later
  work, spec §11.3), so keep the newer image rather than copying snapshot files by hand.
