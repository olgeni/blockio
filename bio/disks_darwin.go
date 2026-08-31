package bio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Disks lists every whole disk diskutil(8) knows about, physical and
// synthesized alike.
//
// On macOS the whole disk that carries the traffic is usually not the
// physical one: an APFS volume's I/O is reported against the synthesized
// container disk it lives in (disk3s5 -> disk3), and the physical disk
// underneath (disk0) stays quiet.  Both are listed; the containers are the
// ones worth watching.
func Disks() ([]Disk, error) {
	var list struct {
		WholeDisks []string
	}
	if err := diskutil(&list, "list", "-plist"); err != nil {
		return nil, err
	}

	var disks []Disk
	for _, name := range list.WholeDisks {
		d, err := Info(name)
		if err != nil {
			continue // no media, or not readable
		}
		disks = append(disks, d)
	}
	sort.Slice(disks, func(i, j int) bool { return lessDevice(disks[i].Name, disks[j].Name) })
	return disks, nil
}

// Info asks diskutil(8) about one device.
func Info(name string) (Disk, error) {
	var info diskInfo
	if err := diskutil(&info, "info", "-plist", name); err != nil {
		return Disk{Name: name, Rotation: -1}, err
	}
	d := info.disk(name)
	if d.MediaSize <= 0 {
		return d, fmt.Errorf("%s: no media", name)
	}
	return d, nil
}

// diskInfo is the part of "diskutil info -plist" that the map needs.
type diskInfo struct {
	Size                int64
	DeviceBlockSize     int64
	MediaName           string
	SolidState          bool
	IORegistryEntryName string
}

// disk turns one diskutil answer into a Disk.
func (i diskInfo) disk(name string) Disk {
	d := Disk{
		Name:       name,
		SectorSize: i.DeviceBlockSize,
		MediaSize:  i.Size,
		Stripe:     i.DeviceBlockSize,
		Descr:      i.MediaName,
		Rotation:   -1,
		// MediaName is inherited from whatever the device is carved out
		// of, so every container on a mounted image calls itself an image
		// too, which is exactly the set to leave unselected.
		Image: i.MediaName == "Disk Image",
	}
	if d.SectorSize > 0 {
		d.Sectors = d.MediaSize / d.SectorSize
	}
	if i.SolidState {
		d.Rotation = 0
	}

	// The name is inherited the same way, so every container on an internal
	// SSD calls itself the SSD.  Say what the device really is.
	switch {
	case i.IORegistryEntryName == "AppleAPFSMedia":
		d.Descr = strings.TrimSpace(i.MediaName + " APFS container")
	case strings.Contains(i.IORegistryEntryName, "Disk Image"):
		d.Descr = "disk image"
	}
	return d
}

// diskutil runs diskutil and unmarshals its plist into v.  There is no
// plist reader in the standard library, so plutil(1) turns it into JSON
// first; both ship with the system.
func diskutil(v any, args ...string) error {
	du := exec.Command("diskutil", args...)
	plist, err := du.Output()
	if err != nil {
		return fmt.Errorf("diskutil %s: %w", strings.Join(args, " "), err)
	}

	pl := exec.Command("plutil", "-convert", "json", "-o", "-", "-")
	pl.Stdin = bytes.NewReader(plist)
	out, err := pl.Output()
	if err != nil {
		return fmt.Errorf("plutil: %w", err)
	}
	return json.Unmarshal(out, v)
}
