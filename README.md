# diskwake

A small Go daemon that keeps specific USB-attached drives spinning during
configured time windows, and leaves them alone the rest of the time so their
own default idle timer can spin them down naturally.

It includes a thin read-only web UI for viewing current configuration,
per-disk runtime status, and live in-process logs.

## Why this exists

USB-SATA bridge chips often
don't reliably honor a long ATA standby timer set via `hdparm -S` or
`smartctl -s standby,N` — the bridge's own internal housekeeping resets the
timer before it ever fires. What *does* reliably work is the bridge's own
factory-default idle timer, which is based on genuine USB bus activity, not
an ATA register.

Rather than fighting the bridge, `diskwake` works with it: during your
configured "awake" windows, it performs a tiny real read directly against
the raw block device every `keepalive_interval`, which resets the bridge's
own idle timer via genuine bus traffic. Outside those windows, it does
nothing at all, and the drive falls asleep on its own after its normal
default idle period.

## How the read works

`diskwake` opens the raw device (e.g. `/dev/disk/by-id/usb-...`) with
`O_DIRECT` and reads the first 4096 bytes with `pread`. `O_DIRECT` bypasses
the kernel page cache, guaranteeing the read actually reaches the physical
drive over USB rather than being served from cache and silently doing
nothing.

## Using diskwake

### Config format

```yaml
disks:
  - name: backup-drive
    device: /dev/disk/backup-drive
    keepalive_interval: 5m
    windows:
      - start: "08:00"
        end: "23:00"
        day: "monday"

  - name: media-drive
    device: /dev/disk/media-drive
    keepalive_interval: 5m
    windows:
      - start: "18:00"
        end: "22:00"
      - start: "07:00"
        end: "09:00"
```

- `device` — the path **inside the container**, not the host. This is a
  name you choose yourself (see "Host vs. container paths" below) — it
  doesn't need to look like a real device path at all, as long as it
  matches what you map in your compose file.
- `keepalive_interval` — any Go duration string (`5m`, `90s`, `1h`, ...).
  Keep this comfortably shorter than the drive's own default idle timeout
  (commonly ~10-30 minutes for USB enclosures) or the drive will still spin
  down inside an "awake" window.
- `windows` — one or more `start`/`end` pairs in 24h `HH:MM`. A window may
  cross midnight (e.g. `start: "22:00"`, `end: "02:00"`). Multiple windows
  per disk are supported. Outside all windows, the disk is left alone
  entirely.
- `day` (optional) — limits a window to a specific weekday (`monday`, `mon`,
  `tuesday`, etc., case-insensitive). If omitted, that window applies every
  day. For a midnight-crossing window (for example `22:00 -> 02:00`), a
  `day: "monday"` window runs from Monday night into early Tuesday.

Times are evaluated in the container's local timezone — set `TZ` in the
compose environment to match your server.

### Host vs. container device paths

This is the one thing worth understanding before setting this up.

- **`config.yaml`** only ever sees a path *inside the container*
  (`/dev/disk/backup-drive` in the example above). This name is arbitrary —
  pick anything descriptive. Because it's arbitrary, `config.yaml` never
  needs to change even if your host reassigns `/dev/sdX` letters after a
  reboot.
- **Your compose file** is the only place that needs your actual,
  host-specific device path, via the `devices:` mapping
  (`host_path:container_path`). Always use the host's stable
  `/dev/disk/by-id/...` path here, never `/dev/sdX` directly:
  ```bash
  ls -l /dev/disk/by-id/
  ```

This split means `config.yaml` and the rest of the project are fully
generic and safe to commit/share as-is — only your local compose file
needs host-specific edits.

### Run with Docker

Create a `compose.yaml` next to `config.yaml` using this minimal service:

```yaml
services:
  diskwake:
    image: ghcr.io/ivanbeke/diskwake:latest
    container_name: diskwake
    restart: unless-stopped
    environment:
      TZ: Etc/UTC
    volumes:
      - ./config.yaml:/etc/diskwake/config.yaml:ro
    ports:
      - "8080:8080"
    devices:
      - "/dev/disk/by-id/REPLACE_WITH_YOUR_DISK_1_ID:/dev/disk/backup-drive"
      - "/dev/disk/by-id/REPLACE_WITH_YOUR_DISK_2_ID:/dev/disk/media-drive"
```

Then:

1. Edit `config.yaml` with your desired windows.
2. Replace `REPLACE_WITH_YOUR_DISK_*_ID` with real host IDs from
   `ls -l /dev/disk/by-id/`.
3. Start it:

```bash
docker compose up -d
docker logs -f diskwake
```

`compose.example.yaml` is kept in this repo as a development-focused local
build template.

No `privileged: true` or extra capabilities are needed — a plain
`O_DIRECT` read only requires normal read access to the device node, which
Docker's `devices:` mapping already grants.

The read-only web UI runs by default on port 8080. Publish the container
port in compose:

```yaml
ports:
  - "8080:8080"
```

To change the UI port, use either CLI flag or env var:

```yaml
command: ["--config", "/etc/diskwake/config.yaml", "--port", "8090"]
```

or

```yaml
environment:
  TZ: Etc/UTC
  DISKWAKE_PORT: "8090"
```

Then open `http://<your-server-ip>:8080` on your LAN.

### Web UI (read-only)

- Enabled by default on `:8080`.
- Override with `--port` or `DISKWAKE_PORT`.
- LAN-accessible when the container/host port is published.
- No authentication is built in.
- Shows current runtime state only (not persisted historical logs).

UI endpoints:

- `/` — dashboard
- `/api/status` — JSON server time/timezone and per-disk status
- `/api/config` — JSON read-only config path/content
- `/api/logs` — JSON current in-memory log buffer
- `/api/logs/stream` — live log stream (Server-Sent Events)

You should see startup log lines listing the loaded disks/windows, followed
by a line every `keepalive_interval` — either `keep-awake read OK` while
inside a window, or `outside configured windows, leaving idle` otherwise.

### Testing changes without waiting

Since the container logs every tick, you can quickly sanity-check a config
change by setting a window that includes right now and a short
`keepalive_interval` (e.g. `30s`), watching the logs, then restarting with
your real values once confirmed:

```bash
docker compose restart diskwake
docker logs -f diskwake
```

### Notes

- This intentionally does *not* touch `smartd`, `hdparm`, or any ATA
  standby timer — it relies entirely on the bridge's own default idle
  behavior, triggered/reset by genuine bus activity.
- If a disk still won't stay awake during its window, try lowering
  `keepalive_interval` — the enclosure's true default idle timeout may be
  shorter than assumed.

## Developing diskwake

### Project layout

```
diskwake/
├── .github/
│   └── workflows/
│       └── docker-publish.yml
├── Dockerfile
├── README.md
├── compose.example.yaml
├── config.go
├── config.yaml
├── config_test.go
├── go.mod
├── go.sum
├── main.go
├── main_test.go
├── reader.go
├── state.go
└── web.go
```

### Local development workflow

Run tests:

```bash
go test ./...
```

Run directly on host (for development):

```bash
go run . -config ./config.yaml -port 8080
```

Build a local binary:

```bash
go build -o diskwake .
```

Build the container image locally:

```bash
docker build -t diskwake:dev .
```

### Publishing image changes

Container publish automation lives in `.github/workflows/docker-publish.yml`.
The workflow runs tests and then builds/pushes/signs the GHCR image on pushes
to `main` and version tags (`v*`).
