# blockio

Watch what the disks are doing, the way DOS defrag used to draw it: one
pane per device, one cell per slice of the address space, green for reads,
red for writes, amber for TRIM. A resilver, a scrub or a big sequential
read shows up as a bright head marching across the map; scattered metadata
writes speckle it. FreeBSD only: the data comes from DTrace's `io`
provider.

## Requirements

- FreeBSD with the DTrace modules loaded: `kldload dtraceall`, or
  `dtraceall_load="YES"` in `/boot/loader.conf`
- root, for both `dtrace(1)` and `diskinfo(8)`
- Go 1.25 or later to build

## Building

```sh
go build -o blockio .
```

## Usage

```sh
sudo ./blockio              # pick devices from a list, all preselected
sudo ./blockio ada0 ada1    # watch these
sudo ./blockio -a           # watch every disk, without asking
sudo ./blockio -l           # list the disks and exit
./blockio -demo 8           # synthetic devices, no root needed
```

Flags:

| Flag    | Meaning                                                   |
| ------- | --------------------------------------------------------- |
| `-a`    | watch every disk, without asking                          |
| `-l`    | list the disks and exit                                   |
| `-i`    | sampling interval (default 100ms)                         |
| `-demo` | synthesize N devices instead of tracing                   |
| `-once` | sample for a while, print one frame, exit (for scripting) |
| `-for`  | how long `-once` samples (default 2s)                     |
| `-size` | frame size for `-once` (default 100x30)                   |

Keys:

| Key     | Action                      |
| ------- | --------------------------- |
| `space` | pause                       |
| `c`     | clear the map               |
| `1`-`9` | show one device full screen |
| `0`     | show all of them again      |
| `q`     | quit                        |

Panes go in one column for one or two devices and two columns above that,
so three devices sit in the four pane layout, five or six in the six, and
seven or eight in the eight.

## How it reads the map

Each pane starts at LBA 0 in the top left and runs left to right, top to
bottom, so the whole device fits in the pane no matter how large it is.
The kernel aggregates I/O into 1024 buckets per device and the display
downsamples those to the cells the terminal gives it.

Every cell carries two things. Activity fades over about half a second, so
the bright part of the map is what the disk is doing right now. Underneath
it a dimmer trail lingers for minutes, showing where the head has been.
Brightness is scaled against a rolling peak per device, and scaled by
square root, so a trickle of metadata is still visible next to a resilver
saturating the disk.

The header of each pane gives the device, its size, what `diskinfo(8)` says
it is, and the current read, write and TRIM rates in bytes and operations
per second.

## The dtrace scripts

The same data is available from the command line, without the TUI:

```sh
sudo dtrace -s iosum.d			# totals by device and command
sudo dtrace -s iosum.d -c "sleep 5"	# for five seconds
sudo ./iosnoop.sh			# one line per I/O, every device
sudo ./iosnoop.sh ada0,ada1		# just these
```

`iosnoop.sh` wraps `iosnoop.d` with the geometry `diskinfo(8)` reports, to
fill in the LBA and POS columns. Both scripts run standalone too, and take
an optional comma separated device list.

## Notes on the FreeBSD io provider

Four things about `io:::start` on FreeBSD are worth knowing, all of them
learned the hard way while writing this:

- `args[0]` and `args[1]` are **not** translated: they arrive as
  `struct bio *` and `struct devstat *`, so `args[0]->b_bcount` does not
  compile. Cast them, or wrap each use in `xlate<bufinfo_t *>()`.
- The size of an I/O is `bio_length`. `bio_bcount` is a buf layer field
  and reads 0 at the GEOM layer.
- `devinfo_t`'s `dev_statname` is just the device name with no unit number,
  so a `== "ada0"` predicate never matches. Use `device_name` together with
  `unit_number`.
- Every logical I/O is seen twice, once at the GEOM layer and once at the
  disk layer. The GEOM layer leaves `devstat` empty and carries the name on
  `bio_to->name` instead; blockio traces only the disk layer, so nothing is
  counted twice.

Also: DTrace's `int` is 32 bits. A media size does not fit in one.
