package bio

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Disks lists every disk in kern.disks that answers diskinfo(8).  Devices
// without media (an empty cd0, say) are left out.
func Disks() ([]Disk, error) {
	out, err := exec.Command("sysctl", "-n", "kern.disks").Output()
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.disks: %w", err)
	}

	var disks []Disk
	for _, name := range strings.Fields(string(out)) {
		d, err := Info(name)
		if err != nil {
			continue // no media, or not readable
		}
		disks = append(disks, d)
	}
	sort.Slice(disks, func(i, j int) bool { return lessDevice(disks[i].Name, disks[j].Name) })
	return disks, nil
}

// Info asks diskinfo(8) about one device.
func Info(name string) (Disk, error) {
	d := Disk{Name: name, Rotation: -1}

	out, err := exec.Command("diskinfo", "-v", "/dev/"+name).Output()
	if err != nil {
		return d, fmt.Errorf("diskinfo %s: %w", name, err)
	}

	for _, line := range strings.Split(string(out), "\n") {
		value, comment, ok := strings.Cut(strings.TrimSpace(line), "#")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch comment = strings.TrimSpace(comment); {
		case strings.HasPrefix(comment, "sectorsize"):
			d.SectorSize, _ = strconv.ParseInt(value, 10, 64)
		case strings.HasPrefix(comment, "mediasize in bytes"):
			d.MediaSize, _ = strconv.ParseInt(value, 10, 64)
		case strings.HasPrefix(comment, "mediasize in sectors"):
			d.Sectors, _ = strconv.ParseInt(value, 10, 64)
		case strings.HasPrefix(comment, "stripesize"):
			d.Stripe, _ = strconv.ParseInt(value, 10, 64)
		case strings.HasPrefix(comment, "Disk descr"):
			d.Descr = value
		case strings.HasPrefix(comment, "Rotation rate"):
			if n, err := strconv.Atoi(value); err == nil {
				d.Rotation = n
			}
		}
	}
	if d.MediaSize <= 0 {
		return d, fmt.Errorf("%s: no media", name)
	}
	return d, nil
}
