//go:build amd64 || arm64

package bio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// From winioctl.h.
const (
	ioctlDiskGetDriveGeometryEx = 0x000700a0
	ioctlStorageQueryProperty   = 0x002d1400

	storageDeviceProperty            = 0
	storageDeviceSeekPenaltyProperty = 7

	busTypeFileBackedVirtual = 0x0f // a mounted VHD or VHDX
)

// Disks lists every \\.\PhysicalDriveN with media, as diskN: the number is
// the one Disk Management and diskpart show, and the one ETW reports.
func Disks() ([]Disk, error) {
	devices, err := dosDevices()
	if err != nil {
		return nil, err
	}

	var disks []Disk
	for _, dev := range devices {
		n, ok := strings.CutPrefix(strings.ToLower(dev), "physicaldrive")
		if !ok {
			continue
		}
		d, err := Info("disk" + n)
		if err != nil {
			continue // no media, or not readable
		}
		disks = append(disks, d)
	}
	sort.Slice(disks, func(i, j int) bool { return lessDevice(disks[i].Name, disks[j].Name) })
	return disks, nil
}

// Info asks one disk about itself: its geometry, what it is called, and
// whether it seeks.  None of that touches the disk's contents, so the
// handle is opened with no access at all and -l works without elevation.
func Info(name string) (Disk, error) {
	d := Disk{Name: name, Rotation: -1}

	n, ok := diskNumber(name)
	if !ok {
		return d, fmt.Errorf("%s: not a disk name (disk0, disk1, ...)", name)
	}
	path, err := windows.UTF16PtrFromString(`\\.\PhysicalDrive` + strconv.FormatUint(uint64(n), 10))
	if err != nil {
		return d, err
	}
	h, err := windows.CreateFile(path, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return d, fmt.Errorf("%s: %w", name, err)
	}
	defer windows.CloseHandle(h)

	// DISK_GEOMETRY_EX: BytesPerSector at 20 and DiskSize at 24, then
	// partition and detection data that are no use here.
	var geom [256]byte
	if _, err := ioctl(h, ioctlDiskGetDriveGeometryEx, nil, geom[:]); err != nil {
		return d, fmt.Errorf("%s: no media: %w", name, err)
	}
	d.SectorSize = int64(binary.LittleEndian.Uint32(geom[20:]))
	d.MediaSize = int64(binary.LittleEndian.Uint64(geom[24:]))
	if d.SectorSize > 0 {
		d.Sectors = d.MediaSize / d.SectorSize
	}
	if d.MediaSize <= 0 {
		return d, fmt.Errorf("%s: no media", name)
	}

	var desc [1024]byte
	if got, err := queryProperty(h, storageDeviceProperty, desc[:]); err == nil {
		var bus uint32
		d.Descr, bus = describe(desc[:got])
		d.Image = bus == busTypeFileBackedVirtual
	}

	// DEVICE_SEEK_PENALTY_DESCRIPTOR: IncursSeekPenalty at 8.  A disk that
	// seeks spins, but at a speed nothing here will say.
	var seek [12]byte
	if got, err := queryProperty(h, storageDeviceSeekPenaltyProperty, seek[:]); err == nil && got > 8 && seek[8] == 0 {
		d.Rotation = 0
	}
	return d, nil
}

// describe reads a STORAGE_DEVICE_DESCRIPTOR: what the device calls
// itself, and the bus it hangs off.
func describe(buf []byte) (descr string, bus uint32) {
	if len(buf) < 32 {
		return "", 0
	}
	str := func(at int) string {
		off := int(binary.LittleEndian.Uint32(buf[at:]))
		if off == 0 || off >= len(buf) {
			return ""
		}
		s, _, _ := bytes.Cut(buf[off:], []byte{0})
		return strings.TrimSpace(string(s))
	}
	vendor, product := str(12), str(16)
	return strings.TrimSpace(vendor + " " + product), binary.LittleEndian.Uint32(buf[28:])
}

// queryProperty asks IOCTL_STORAGE_QUERY_PROPERTY for one property of the
// device itself.
func queryProperty(h windows.Handle, id uint32, out []byte) (int, error) {
	var query [12]byte // STORAGE_PROPERTY_QUERY: PropertyId, then QueryType 0, "standard"
	binary.LittleEndian.PutUint32(query[0:], id)
	return ioctl(h, ioctlStorageQueryProperty, query[:], out)
}

func ioctl(h windows.Handle, code uint32, in, out []byte) (int, error) {
	var inp *byte
	if len(in) > 0 {
		inp = &in[0]
	}
	var got uint32
	err := windows.DeviceIoControl(h, code, inp, uint32(len(in)), &out[0], uint32(len(out)), &got, nil)
	return int(got), err
}

// dosDevices lists the DOS device names: C:, NUL, PhysicalDrive0 and the
// rest, which is where the disks are to be found without SetupAPI.
func dosDevices() ([]string, error) {
	for size := 1 << 16; size <= 1<<24; size <<= 1 {
		buf := make([]uint16, size)
		n, err := windows.QueryDosDevice(nil, &buf[0], uint32(len(buf)))
		if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("QueryDosDevice: %w", err)
		}

		var names []string
		start := 0
		for i, c := range buf[:n] {
			if c == 0 {
				if i > start {
					names = append(names, windows.UTF16ToString(buf[start:i]))
				}
				start = i + 1
			}
		}
		return names, nil
	}
	return nil, errors.New("QueryDosDevice: too many devices")
}

// diskNumber reads the N out of diskN.
func diskNumber(name string) (uint32, bool) {
	digits, ok := strings.CutPrefix(name, "disk")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(digits, 10, 32)
	return uint32(n), err == nil
}
