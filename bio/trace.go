package bio

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Buckets is how finely the io provider splits each device.  The kernel
// aggregates into this many bins per device and the display downsamples
// to whatever the terminal gives us.
const Buckets = 1024

// Command is a bio_cmd value; see /usr/lib/dtrace/io.d.
type Command int

const (
	CmdRead   Command = 1
	CmdWrite  Command = 2
	CmdDelete Command = 3 // TRIM/UNMAP
	CmdFlush  Command = 5
)

func (c Command) String() string {
	switch c {
	case CmdRead:
		return "READ"
	case CmdWrite:
		return "WRITE"
	case CmdDelete:
		return "TRIM"
	case CmdFlush:
		return "FLUSH"
	}
	return "CMD" + strconv.Itoa(int(c))
}

// Cell is the bytes one command moved inside one bucket of one device.
type Cell struct {
	Dev    string
	Cmd    Command
	Bucket int
	Bytes  int64
}

// Stat totals one command on one device over the frame.
type Stat struct {
	Dev   string
	Cmd   Command
	Ops   int64
	Bytes int64
}

// Frame is one tick's worth of activity: what moved, and where.
type Frame struct {
	Cells []Cell
	Stats []Stat
}

// script is the D program.  The device table is generated: diskinfo(8)
// knows the media size, the io provider's disk layer does not.
const script = `
#pragma D option quiet
#pragma D option bufsize=8m
#pragma D option aggsize=16m
#pragma D option dynvarsize=8m
#pragma D option switchrate=%dms
#pragma D option aggrate=%dms

inline int BUCKETS = %d;

int64_t media[string];		/* int would truncate at 4G */
int track[string];

BEGIN
{
%s}

/*
 * Only the disk layer: it names the device through devstat, while the
 * GEOM layer above it reports the same I/O again under the provider name.
 */
io:::start
/track[strjoin(stringof(((struct devstat *)arg1)->device_name),
               lltostr(((struct devstat *)arg1)->unit_number))] != 0/
{
	this->dev = strjoin(stringof(((struct devstat *)arg1)->device_name),
	    lltostr(((struct devstat *)arg1)->unit_number));
	this->cmd = ((struct bio *)arg0)->bio_cmd;
	this->len = ((struct bio *)arg0)->bio_length;
	this->bucket = (((struct bio *)arg0)->bio_offset * BUCKETS) / media[this->dev];

	@cells[this->dev, this->cmd, this->bucket] = sum(this->len);
	@ops[this->dev, this->cmd] = count();
	@bytes[this->dev, this->cmd] = sum(this->len);
}

tick-%dms
{
	printa("C %%s %%d %%d %%@d\n", @cells);
	printa("S %%s %%d %%@d %%@d\n", @ops, @bytes);
	printf("T\n");

	trunc(@cells);
	trunc(@ops);
	trunc(@bytes);
}
`

// Script returns the D program that traces the given disks.
func Script(disks []Disk, intervalMS int) string {
	var table strings.Builder
	for _, d := range disks {
		fmt.Fprintf(&table, "\tmedia[\"%s\"] = %d;\n", d.Name, d.MediaSize)
		fmt.Fprintf(&table, "\ttrack[\"%s\"] = 1;\n", d.Name)
	}
	half := intervalMS / 2
	if half < 10 {
		half = 10
	}
	return fmt.Sprintf(script, half, half, Buckets, table.String(), intervalMS)
}

// Tracer runs dtrace and turns its output into frames.
type Tracer struct {
	Disks      []Disk
	IntervalMS int

	cmd    *exec.Cmd
	stderr strings.Builder
	mu     sync.Mutex
}

// Start runs dtrace until the context is cancelled.  Frames arrive on the
// returned channel, which closes when tracing stops; the error channel then
// carries whatever went wrong, or nil.
func (t *Tracer) Start(ctx context.Context) (<-chan Frame, <-chan error, error) {
	interval := t.IntervalMS
	if interval <= 0 {
		interval = 100
	}

	file, err := os.CreateTemp("", "blockio-*.d")
	if err != nil {
		return nil, nil, err
	}
	if _, err := file.WriteString(Script(t.Disks, interval)); err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, nil, err
	}
	file.Close()

	cmd := exec.CommandContext(ctx, "dtrace", "-s", file.Name())
	cmd.Stderr = &t.stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		os.Remove(file.Name())
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		os.Remove(file.Name())
		return nil, nil, fmt.Errorf("dtrace: %w", err)
	}
	t.cmd = cmd

	frames := make(chan Frame, 8)
	errs := make(chan error, 1)

	go func() {
		defer close(frames)
		scan := bufio.NewScanner(stdout)
		scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

		var frame Frame
		for scan.Scan() {
			line := scan.Text()
			if line == "T" {
				select {
				case frames <- frame:
				case <-ctx.Done():
					return
				default: // display is behind; drop rather than stall dtrace
				}
				frame = Frame{}
				continue
			}
			parse(line, &frame)
		}
	}()

	go func() {
		defer close(errs)
		defer os.Remove(file.Name())
		err := cmd.Wait()
		if ctx.Err() != nil { // we asked it to stop
			errs <- nil
			return
		}
		if err != nil {
			errs <- fmt.Errorf("dtrace: %w: %s", err, strings.TrimSpace(t.stderr.String()))
			return
		}
		errs <- nil
	}()

	return frames, errs, nil
}

// Warnings returns whatever dtrace has written to stderr so far.
func (t *Tracer) Warnings() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(t.stderr.String())
}

func parse(line string, frame *Frame) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return
	}
	switch f[0] {
	case "C":
		if len(f) != 5 {
			return
		}
		cmd, err1 := strconv.Atoi(f[2])
		bucket, err2 := strconv.Atoi(f[3])
		bytes, err3 := strconv.ParseInt(f[4], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return
		}
		if bucket < 0 || bucket >= Buckets {
			return
		}
		frame.Cells = append(frame.Cells, Cell{f[1], Command(cmd), bucket, bytes})
	case "S":
		if len(f) != 5 {
			return
		}
		cmd, err1 := strconv.Atoi(f[2])
		ops, err2 := strconv.ParseInt(f[3], 10, 64)
		bytes, err3 := strconv.ParseInt(f[4], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return
		}
		frame.Stats = append(frame.Stats, Stat{f[1], Command(cmd), ops, bytes})
	}
}
