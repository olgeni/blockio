package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/olgeni/blockio/bio"
	"github.com/olgeni/blockio/ui"
)

// configPath is $BLOCKIO_CONFIG, else $XDG_CONFIG_HOME/blockio/config, else
// ~/.config/blockio/config.
func configPath() string {
	if p := os.Getenv("BLOCKIO_CONFIG"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "blockio", "config")
}

// loadConfig folds the config file into cfg.  A missing file is not an
// error; a bad line is a warning, never fatal.
func loadConfig(path string, cfg *ui.Config, interval *time.Duration) []string {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return []string{fmt.Sprintf("%s: %v", path, err)}
		}
		return nil
	}
	defer file.Close()

	var warnings []string
	scan := bufio.NewScanner(file)
	for line := 1; scan.Scan(); line++ {
		text := scan.Text()
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s:%d: not an option = value line", path, line))
			continue
		}
		if err := setOption(cfg, interval, strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s:%d: %v", path, line, err))
		}
	}
	return warnings
}

// setOption applies one "key = value" pair; the flags use the same parsers.
func setOption(cfg *ui.Config, interval *time.Duration, key, value string) error {
	switch key {
	case "color":
		mode, err := ui.ParseColorMode(value)
		if err != nil {
			return err
		}
		cfg.Color = mode
	case "scale":
		scale, err := parseScale(value)
		if err != nil {
			return err
		}
		cfg.Scale = scale
	case "thresholds":
		steps, err := ui.ParseThresholds(value)
		if err != nil {
			return err
		}
		cfg.Thresholds = steps
	case "decay":
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return fmt.Errorf("bad decay %q", value)
		}
		cfg.HeatLife = d
	case "trail":
		if value == "off" || value == "none" {
			cfg.TrailLife = 0
			return nil
		}
		d, err := time.ParseDuration(value)
		if err != nil || d < 0 {
			return fmt.Errorf("bad trail %q", value)
		}
		cfg.TrailLife = d
	case "buckets":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("bad buckets %q", value)
		}
		cfg.Buckets = bio.ClampBuckets(n)
	case "half":
		on, err := parseBool(value)
		if err != nil {
			return err
		}
		cfg.HalfBlocks = on
	case "interval":
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return fmt.Errorf("bad interval %q", value)
		}
		*interval = d
	default:
		return fmt.Errorf("unknown option %q", key)
	}
	return nil
}

func parseScale(s string) (ui.Scale, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "auto", "":
		return ui.ScaleAuto, nil
	case "fixed", "absolute":
		return ui.ScaleFixed, nil
	}
	return ui.ScaleAuto, fmt.Errorf("bad scale %q (auto, fixed)", s)
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "yes", "true", "1":
		return true, nil
	case "off", "no", "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("bad boolean %q", s)
}
