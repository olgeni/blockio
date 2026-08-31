//go:build freebsd || darwin

package bio

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Tracer runs dtrace(1) and turns its output into frames.  The D program
// itself is per-platform (the io provider does not look the same on FreeBSD
// and macOS); what it prints is not:
//
//	C <dev> <cmd> <bucket> <bytes>	bytes moved in one slice of a device
//	S <dev> <cmd> <ops> <bytes>	the frame's totals
//	T				end of frame
type Tracer struct {
	Disks      []Disk
	IntervalMS int
	Buckets    int

	cmd    *exec.Cmd
	stderr safeBuilder
}

// Start runs dtrace until the context is cancelled.
func (t *Tracer) Start(ctx context.Context) (<-chan Frame, <-chan error, error) {
	interval := t.IntervalMS
	if interval <= 0 {
		interval = 100
	}
	buckets := t.Buckets
	if buckets <= 0 {
		buckets = DefaultBuckets
	}

	file, err := os.CreateTemp("", "blockio-*.d")
	if err != nil {
		return nil, nil, err
	}
	if _, err := file.WriteString(Script(t.Disks, interval, buckets)); err != nil {
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
			parse(line, &frame, buckets)
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
			errs <- fmt.Errorf("dtrace: %w: %s", err, t.stderr.String())
			return
		}
		errs <- nil
	}()

	return frames, errs, nil
}

// Warnings returns whatever dtrace has written to stderr so far.
func (t *Tracer) Warnings() string { return t.stderr.String() }

func parse(line string, frame *Frame, buckets int) {
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
		if bucket < 0 || bucket >= buckets {
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
