# diskwake

A small Go daemon that keeps specific USB-attached drives spinning during
configured time windows, and leaves them alone the rest of the time so their
own default idle timer can spin them down naturally.

## Why this exists

USB-SATA bridge chips (like the one in this WD easystore enclosure) often
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

## Config format

```yaml
disks:
  - name: backup-drive
    device: /dev/disk/backup-drive
    keepalive_interval: 5m
    windows:
      - start: "08:00"
        end: "23:00"

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
  matches what you map in `docker-compose.yml`.
- `keepalive_interval` — any Go duration string (`5m`, `90s`, `1h`, ...).
  Keep this comfortably shorter than the drive's own default idle timeout
  (commonly ~10-30 minutes for USB enclosures) or the drive will still spin
  down inside an "awake" window.
- `windows` — one or more `start`/`end` pairs in 24h `HH:MM`. A window may
  cross midnight (e.g. `start: "22:00"`, `end: "02:00"`). Multiple windows
  per disk are supported. Outside all windows, the disk is left alone
  entirely.

Times are evaluated in the container's local timezone — set `TZ` in the
compose environment to match your server.

## Host vs. container device paths

This is the one thing worth understanding before setting this up.

- **`config.yaml`** only ever sees a path *inside the container*
  (`/dev/disk/backup-drive` in the example above). This name is arbitrary —
  pick anything descriptive. Because it's arbitrary, `config.yaml` never
  needs to change even if your host reassigns `/dev/sdX` letters after a
  reboot.
- **`docker-compose.yml`** is the only place that needs your actual,
  host-specific device path, via the `devices:` mapping
  (`host_path:container_path`). Always use the host's stable
  `/dev/disk/by-id/...` path here, never `/dev/sdX` directly:
  ```bash
  ls -l /dev/disk/by-id/
  ```

This split means `config.yaml` and the rest of the project are fully
generic and safe to commit/share as-is — only your local
`docker-compose.yml` needs host-specific edits.

## Building and running

Project layout:

```
services/
├── docker-compose.yml       <- your compose file; add the service block below
└── diskwake/
    ├── Dockerfile
    ├── go.mod
    ├── go.sum
    ├── main.go
    ├── config.go
    ├── reader.go
    └── config.yaml          <- edit disk names/windows for your setup
```

`config.yaml` ships with generic placeholder disks (`backup-drive`,
`media-drive`) — edit the names and windows to fit your setup, but you can
leave the `device:` values as-is unless you want different names; they're
just internal labels, not real paths.

Add this service block to your `docker-compose.yml`, replacing the two
`REPLACE_WITH_YOUR_DISK_*_ID` placeholders with your actual host device IDs
from `ls -l /dev/disk/by-id/` (a filled-in template is also provided as
`docker-compose.yml.example`):

```yaml
  diskwake:
    build: ./diskwake
    container_name: diskwake
    restart: unless-stopped
    environment:
      TZ: Etc/UTC  # set to your local timezone
    volumes:
      - ./diskwake/config.yaml:/etc/diskwake/config.yaml:ro
    devices:
      - "/dev/disk/by-id/REPLACE_WITH_YOUR_DISK_1_ID:/dev/disk/backup-drive"
      - "/dev/disk/by-id/REPLACE_WITH_YOUR_DISK_2_ID:/dev/disk/media-drive"
```

No `privileged: true` or extra capabilities are needed — a plain
`O_DIRECT` read only requires normal read access to the device node, which
Docker's `devices:` mapping already grants.

Then:

```bash
docker compose up -d --build diskwake
docker logs -f diskwake
```

You should see startup log lines listing the loaded disks/windows, followed
by a line every `keepalive_interval` — either `keep-awake read OK` while
inside a window, or `outside configured windows, leaving idle` otherwise.

## Testing changes without waiting

Since the container logs every tick, you can quickly sanity-check a config
change by setting a window that includes right now and a short
`keepalive_interval` (e.g. `30s`), watching the logs, then restarting with
your real values once confirmed:

```bash
docker compose restart diskwake
docker logs -f diskwake
```

## Notes

- This intentionally does *not* touch `smartd`, `hdparm`, or any ATA
  standby timer — it relies entirely on the bridge's own default idle
  behavior, triggered/reset by genuine bus activity.
- If a disk still won't stay awake during its window, try lowering
  `keepalive_interval` — the enclosure's true default idle timeout may be
  shorter than assumed.
