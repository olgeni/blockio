// blockio shows what the disks are doing, as a defrag-style map: one cell
// per slice of each device, green for reads, red for writes, amber for TRIM.
//
//	blockio                 pick devices interactively
//	blockio ada0 ada1       watch these
//	blockio -a              watch every disk
//
// It needs root either way, and on FreeBSD the dtrace modules as well
// (kldload dtraceall): the io provider is read through dtrace(1).  On macOS
// it reads fs_usage(1) instead, which needs nothing turned off.
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
	"github.com/charmbracelet/x/term"

	"github.com/olgeni/blockio/bio"
	"github.com/olgeni/blockio/ui"
)

func main() {
	def := ui.DefaultConfig()
	var (
		all      = flag.Bool("a", false, "watch every disk, without asking")
		list     = flag.Bool("l", false, "list the disks and exit")
		interval = flag.Duration("i", 100*time.Millisecond, "sampling interval")
		once     = flag.Bool("once", false, "sample for a while, print one frame, exit")
		window   = flag.Duration("for", 2*time.Second, "how long -once samples")
		size     = flag.String("size", "100x30", "frame size for -once")
		demo     = flag.Int("demo", 0, "synthesize N devices instead of tracing (for looking at layouts)")
		source   = flag.String("source", "auto", "where the I/O comes from: auto, dtrace, fsusage (macOS)")

		color      = flag.String("color", "auto", "color: auto, truecolor, 256, 16, off")
		scale      = flag.String("scale", "auto", "color scale: auto (per device) or fixed (thresholds)")
		thresholds = flag.String("thresholds", "64K,512K,4M,32M", "hot/cold steps, bytes per second per cell")
		decay      = flag.Duration("decay", def.HeatLife, "half-life of activity on the map")
		trail      = flag.Duration("trail", def.TrailLife, "half-life of the trail (0 for none)")
		buckets    = flag.Int("buckets", 0, "slices per device (0 fits the terminal)")
		half       = flag.Bool("half", true, "half blocks: two rows of cells per terminal row")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [-a] [-l] [-i interval] [device ...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	// The config file sets defaults; flags actually given beat it.
	cfg := def
	kind := bio.SourceAuto
	warnings := loadConfig(configPath(), &cfg, interval, &kind)

	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for name, value := range map[string]string{
		"color": *color, "scale": *scale, "thresholds": *thresholds,
		"source": *source,
	} {
		if set[name] {
			if err := setOption(&cfg, interval, &kind, name, value); err != nil {
				warnings = append(warnings, err.Error())
			}
		}
	}
	if set["decay"] {
		cfg.HeatLife = *decay
	}
	if set["trail"] {
		cfg.TrailLife = *trail
	}
	if set["buckets"] {
		cfg.Buckets = bio.ClampBuckets(*buckets)
	}
	if set["half"] {
		cfg.HalfBlocks = *half
	}
	if !set["buckets"] && cfg.Buckets == def.Buckets {
		cfg.Buckets = autoBuckets(cfg.HalfBlocks)
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "blockio: %s\n", w)
	}

	if err := run(*all, *list, *once, *demo, kind, *interval, *window, *size, cfg, flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "blockio: %v\n", err)
		os.Exit(1)
	}
}

// autoBuckets asks for about as many slices per device as the terminal can
// draw, so the map is not coarser than the screen.  The kernel aggregates
// into these bins, so this is fixed for the run: resize much larger and the
// map goes blocky until the next start.
func autoBuckets(halfBlocks bool) int {
	width, height, err := term.GetSize(os.Stdout.Fd())
	if err != nil || width <= 0 || height <= 0 {
		return bio.DefaultBuckets
	}
	rows := height
	if halfBlocks {
		rows *= 2
	}
	want := 1
	for want < width*rows {
		want <<= 1
	}
	return bio.ClampBuckets(want)
}

func run(all, list, once bool, demo int, kind bio.SourceKind, interval, window time.Duration, size string, cfg ui.Config, args []string) error {
	if demo > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		disks, frames, errs := bio.Demo(ctx, demo, cfg.Buckets, interval)
		model := ui.New(disks, frames, errs, interval, cfg)
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

	if err := bio.CheckSource(kind); err != nil {
		return err
	}
	source, err := bio.NewSource(kind, watch, int(interval.Milliseconds()), cfg.Buckets)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frames, errs, err := source.Start(ctx)
	if err != nil {
		return err
	}

	model := ui.New(watch, frames, errs, interval, cfg)
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

	// Everything is offered, but a disk image starts unselected: macOS
	// synthesizes a device for every mounted one, and a machine with a few
	// simulator runtimes about has more of those than it has disks.
	options := make([]huh.Option[string], 0, len(disks))
	for _, d := range disks {
		label := fmt.Sprintf("%-8s %8s  %s", d.Name, d.Size(), d.Descr)
		options = append(options, huh.NewOption(label, d.Name).Selected(!d.Image))
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
