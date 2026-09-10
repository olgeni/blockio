//go:build amd64 || arm64

package bio

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

// The ETW structures are written out by hand, so hold them to the sizes and
// offsets the SDK headers give on 64-bit Windows.
func TestETWLayout(t *testing.T) {
	var lf eventTraceLogfile
	var rec eventRecord
	for _, c := range []struct {
		what      string
		got, want uintptr
	}{
		{"EVENT_TRACE_PROPERTIES", unsafe.Sizeof(eventTraceProperties{}), 120},
		{"EVENT_TRACE_LOGFILEW", unsafe.Sizeof(lf), 448},
		{"EVENT_TRACE_LOGFILEW.BufferCallback", unsafe.Offsetof(lf.BufferCallback), 400},
		{"EVENT_TRACE_LOGFILEW.EventRecordCallback", unsafe.Offsetof(lf.EventRecordCallback), 424},
		{"EVENT_TRACE_LOGFILEW.Context", unsafe.Offsetof(lf.Context), 440},
		{"EVENT_HEADER", unsafe.Sizeof(rec.EventHeader), 80},
		{"EVENT_HEADER.ProviderId", unsafe.Offsetof(rec.EventHeader.ProviderID), 24},
		{"EVENT_HEADER.EventDescriptor", unsafe.Offsetof(rec.EventHeader.EventDescriptor), 40},
		{"EVENT_RECORD", unsafe.Sizeof(rec), 112},
		{"EVENT_RECORD.UserDataLength", unsafe.Offsetof(rec.UserDataLength), 86},
		{"EVENT_RECORD.UserData", unsafe.Offsetof(rec.UserData), 96},
	} {
		if c.got != c.want {
			t.Errorf("%s is %d, want %d", c.what, c.got, c.want)
		}
	}
}

func TestDiskEvent(t *testing.T) {
	// DiskNumber, IrpFlags, TransferSize, Reserved, ByteOffset, then two
	// pointers and HighResResponseTime.
	payload := make([]byte, 48)
	binary.LittleEndian.PutUint32(payload[0:], 2)
	binary.LittleEndian.PutUint32(payload[4:], 0x60043)
	binary.LittleEndian.PutUint32(payload[8:], 0x1000)
	binary.LittleEndian.PutUint64(payload[16:], 0x12345000)

	disk, cmd, offset, bytes, ok := diskEvent(kernelDiskWrite, payload)
	if !ok || disk != 2 || cmd != CmdWrite || offset != 0x12345000 || bytes != 0x1000 {
		t.Errorf("write = %d %v %#x %#x %v, want 2 WRITE 0x12345000 0x1000 true",
			disk, cmd, offset, bytes, ok)
	}
	if _, cmd, _, _, ok := diskEvent(kernelDiskRead, payload); !ok || cmd != CmdRead {
		t.Errorf("read = %v %v, want READ", cmd, ok)
	}
	if _, _, _, _, ok := diskEvent(14, payload); ok {
		t.Error("a flush should be left out")
	}
	if _, _, _, _, ok := diskEvent(kernelDiskRead, payload[:20]); ok {
		t.Error("a short payload should be left out")
	}
	binary.LittleEndian.PutUint32(payload[8:], 0)
	if _, _, _, _, ok := diskEvent(kernelDiskRead, payload); ok {
		t.Error("an empty transfer should be left out")
	}
}
