// blockio shows what the disks are doing, as a defrag-style map: one cell
// per slice of each device, green for reads, red for writes, amber for TRIM.
//
//	blockio                 pick devices interactively
//	blockio ada0 ada1       watch these
//	blockio -a              watch every disk
//
// It reads the io provider through dtrace(1), so it needs root and the
// dtrace modules (kldload dtraceall).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/olgeni/blockio/bio"
	"github.com/olgeni/blockio/ui"
)

func main() {
	var (
		all      = flag.Bool("a", false, "watch every disk, without asking")
		list     = flag.Bool("l", false, "list the disks and exit")
		interval = flag.Duration("i", 100*time.Millisecond, "sampling interval")
		once     = flag.Bool("once", false, "sample for a while, print one frame, exit")
		window   = flag.Duration("for", 2*time.Second, "how long -once samples")
		size     = flag.String("size", "100x30", "frame size for -once")
		demo     = flag.Int("demo", 0, "synthesize N devices instead of tracing (for looking at layouts)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [-a] [-l] [-i interval] [device ...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if err := run(*all, *list, *once, *demo, *interval, *window, *size, flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "blockio: %v\n", err)
		os.Exit(1)
	}
}

func run(all, list, once bool, demo int, interval, window time.Duration, size string, args []string) error {
	if demo > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		disks, frames, errs := bio.Demo(ctx, demo, interval)
		model := ui.New(disks, frames, errs, interval)
		if once {
			return sample(&model, frames, errs, window, size)
		}
		_, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
		return err
	}

	disks, err := bio.Disks()
	if err != nil {
		return err
	}
	if len(disks) == 0 {
		return errors.New("no disks with media")
	}

	if list {
		fmt.Printf("%-10s %8s %10s %18s %s\n", "DEVICE", "SIZE", "SECTOR", "MEDIASIZE", "DESCRIPTION")
		for _, d := range disks {
			fmt.Printf("%-10s %8s %10d %18d %s\n", d.Name, d.Size(), d.SectorSize, d.MediaSize, d.Descr)
		}
		return nil
	}

	watch, err := choose(disks, args, all || once)
	if err != nil {
		return err
	}
	if len(watch) == 0 {
		return nil // nothing picked
	}

	if err := checkDtrace(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracer := &bio.Tracer{Disks: watch, IntervalMS: int(interval.Milliseconds())}
	frames, errs, err := tracer.Start(ctx)
	if err != nil {
		return err
	}

	model := ui.New(watch, frames, errs, interval)
	if once {
		return sample(&model, frames, errs, window, size)
	}

	_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
	cancel()
	return err
}

// choose resolves the devices to watch: named on the command line, all of
// them, or picked from a list.
func choose(disks []bio.Disk, args []string, all bool) ([]bio.Disk, error) {
	byName := make(map[string]bio.Disk, len(disks))
	for _, d := range disks {
		byName[d.Name] = d
	}

	if len(args) > 0 {
		var out []bio.Disk
		for _, arg := range args {
			name := strings.TrimPrefix(arg, "/dev/")
			d, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("%s: not a disk with media (try -l)", arg)
			}
			out = append(out, d)
		}
		return out, nil
	}

	if all || len(disks) == 1 {
		return disks, nil
	}

	options := make([]huh.Option[string], 0, len(disks))
	for _, d := range disks {
		label := fmt.Sprintf("%-8s %8s  %s", d.Name, d.Size(), d.Descr)
		options = append(options, huh.NewOption(label, d.Name).Selected(true))
	}

	var picked []string
	form := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Which devices?").
			Description("space toggles, enter starts").
			Options(options...).
			Value(&picked),
	))
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil, nil
		}
		return nil, err
	}

	var out []bio.Disk
	for _, name := range picked {
		out = append(out, byName[name])
	}
	return out, nil
}

// checkDtrace explains the two usual reasons dtrace will not start.
func checkDtrace() error {
	if os.Geteuid() != 0 {
		return errors.New("needs root for dtrace(1): try sudo blockio")
	}
	if _, err := os.Stat("/dev/dtrace"); err != nil {
		return errors.New("/dev/dtrace is missing: kldload dtraceall")
	}
	return nil
}

// sample drives the model without a terminal, for -once.
func sample(model *ui.Model, frames <-chan bio.Frame, errs <-chan error, window time.Duration, size string) error {
	var w, h int
	if _, err := fmt.Sscanf(size, "%dx%d", &w, &h); err != nil || w < 20 || h < 8 {
		return fmt.Errorf("bad -size %q, want WxH", size)
	}
	model.SetSize(w, h)

	deadline := time.After(window)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				fmt.Print(model.View())
				return nil
			}
			model.Apply(f)
		case err := <-errs:
			if err != nil {
				return err
			}
		case <-deadline:
			fmt.Println(model.View())
			return nil
		}
	}
}
