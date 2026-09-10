//go:build amd64 || arm64

package bio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ETW watches disks through Event Tracing for Windows.  The
// Microsoft-Windows-Kernel-Disk provider raises an event for every
// completed disk I/O:
//
//	DiskNumber  IrpFlags  TransferSize  Reserved  ByteOffset  FileObject ...
//
// The disk is the N of \\.\PhysicalDriveN and the offset is in bytes from
// the start of the disk, which is all the map needs.  Like fs_usage it
// reports one I/O at a time and leaves the aggregating to us, but there is
// no subprocess: the session is started and read in-process.
//
// The provider knows reads, writes and flushes and nothing else, so there
// is no TRIM: the amber layer stays empty.
type ETW struct {
	Disks      []Disk
	IntervalMS int
	Buckets    int

	acc      *accumulator
	byNumber map[uint32]string // disk number to name
}

// sessionName is what the session is called while it runs, in "logman
// query -ets" and everywhere else.  There is only the one, so a second
// blockio takes it over from the first.
const sessionName = "blockio"

var sessionName16, _ = windows.UTF16FromString(sessionName)

// kernelDisk is Microsoft-Windows-Kernel-Disk.
var kernelDisk = windows.GUID{
	Data1: 0xc7bde69a, Data2: 0xe1e0, Data3: 0x4177,
	Data4: [8]byte{0xb6, 0xef, 0x28, 0x3a, 0xd1, 0x52, 0x52, 0x71},
}

// Its event IDs; 14 is a flush, which the map has no use for.
const (
	kernelDiskRead  = 10
	kernelDiskWrite = 11
)

// From evntrace.h and evntcons.h.
const (
	wnodeFlagTracedGUID            = 0x00020000
	eventTraceRealTimeMode         = 0x00000100
	eventTraceControlQuery         = 0
	eventTraceControlStop          = 1
	eventTraceControlFlush         = 3
	eventControlCodeEnableProvider = 1
	traceLevelInformation          = 4
	processTraceModeRealTime       = 0x00000100
	processTraceModeEventRecord    = 0x10000000
	invalidProcessTraceHandle      = ^uintptr(0)
)

var (
	advapi32           = windows.NewLazySystemDLL("advapi32.dll")
	procStartTraceW    = advapi32.NewProc("StartTraceW")
	procControlTraceW  = advapi32.NewProc("ControlTraceW")
	procEnableTraceEx2 = advapi32.NewProc("EnableTraceEx2")
	procOpenTraceW     = advapi32.NewProc("OpenTraceW")
	procProcessTrace   = advapi32.NewProc("ProcessTrace")
	procCloseTrace     = advapi32.NewProc("CloseTrace")
)

// etwActive is the ETW the callback feeds.  ProcessTrace calls a bare C
// function pointer with no room for a closure, so this is how it finds its
// way back; one session per process is all blockio wants anyway.
var etwActive atomic.Pointer[ETW]

// etwCallback is etwEvent as ProcessTrace wants it.  Callbacks cannot be
// freed, so there is only ever the one.
var etwCallback = windows.NewCallback(etwEvent)

// Start opens the session and reads it until the context is cancelled.
func (e *ETW) Start(ctx context.Context) (<-chan Frame, <-chan error, error) {
	interval := e.IntervalMS
	if interval <= 0 {
		interval = 100
	}
	e.acc = newAccumulator(e.Disks, e.Buckets)
	e.byNumber = make(map[uint32]string, len(e.Disks))
	for _, d := range e.Disks {
		if n, ok := diskNumber(d.Name); ok {
			e.byNumber[n] = d.Name
		}
	}

	if !etwActive.CompareAndSwap(nil, e) {
		return nil, nil, errors.New("etw: already watching")
	}
	if err := startSession(); err != nil {
		etwActive.Store(nil)
		return nil, nil, err
	}
	trace, logfile, err := openTrace()
	if err != nil {
		stopSession()
		etwActive.Store(nil)
		return nil, nil, err
	}

	done := make(chan struct{})
	var processErr error
	go func() {
		defer close(done)
		processErr = processTrace(trace)
		runtime.KeepAlive(logfile)
	}()

	// ETW delivers events, not frames, so the clock is ours.  It also hands
	// the reader a buffer only when the buffer fills, or once a second at
	// the most often, which would show a quiet disk ten frames late: so
	// the session is flushed every interval as well.
	period := time.Duration(interval) * time.Millisecond
	frames := e.acc.run(ctx, period, done)
	go flushEvery(ctx, period, done)

	errs := make(chan error, 1)
	go func() {
		defer close(errs)
		select {
		case <-ctx.Done():
		case <-done:
		}
		// Stopping the session is what makes ProcessTrace return; left
		// alone, it would outlive this process and go on collecting.
		stopSession()
		closeTrace(trace)
		<-done
		etwActive.Store(nil)

		switch {
		case ctx.Err() != nil: // we asked it to stop
			errs <- nil
		case processErr != nil:
			errs <- fmt.Errorf("etw: %w", processErr)
		default:
			errs <- errors.New("etw: the session was stopped from outside (by another blockio?)")
		}
	}()

	return frames, errs, nil
}

// Warnings reports what the session has had to throw away: ETW does not
// hold up a busy disk for a slow reader, it drops buffers instead.
func (e *ETW) Warnings() string {
	p, err := controlSession(eventTraceControlQuery)
	if err != nil || p.EventsLost+p.RealTimeBuffersLost == 0 {
		return ""
	}
	return fmt.Sprintf("%d events and %d buffers lost", p.EventsLost, p.RealTimeBuffersLost)
}

// etwEvent is called by ProcessTrace, on its thread, for every event.
func etwEvent(rec *eventRecord) uintptr {
	e := etwActive.Load()
	if e == nil || rec.EventHeader.ProviderID != kernelDisk {
		return 0
	}
	data := unsafe.Slice((*byte)(rec.UserData), rec.UserDataLength)
	disk, cmd, offset, bytes, ok := diskEvent(rec.EventHeader.EventDescriptor.ID, data)
	if !ok {
		return 0
	}
	if dev, ok := e.byNumber[disk]; ok {
		e.acc.add(dev, cmd, offset, bytes)
	}
	return 0
}

// diskEvent reads a Kernel-Disk read or write event.  Everything wanted
// sits ahead of the first pointer in the payload, so the offsets are the
// same whatever the width of the kernel that wrote it.
func diskEvent(id uint16, data []byte) (disk uint32, cmd Command, offset, bytes int64, ok bool) {
	switch id {
	case kernelDiskRead:
		cmd = CmdRead
	case kernelDiskWrite:
		cmd = CmdWrite
	default:
		return
	}
	if len(data) < 24 {
		return 0, 0, 0, 0, false
	}
	disk = binary.LittleEndian.Uint32(data[0:])
	bytes = int64(binary.LittleEndian.Uint32(data[8:]))
	offset = int64(binary.LittleEndian.Uint64(data[16:]))
	if bytes <= 0 {
		return 0, 0, 0, 0, false
	}
	return disk, cmd, offset, bytes, true
}

// startSession starts the real-time session and turns the provider on.
func startSession() error {
	var handle uint64
	err := startTrace(&handle, sessionProps())
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		// A blockio that never got to clean up (killed, or crashed)
		// leaves its session behind, running with nobody reading it.
		stopSession()
		err = startTrace(&handle, sessionProps())
	}
	switch {
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return errors.New("etw: starting a trace session needs an elevated prompt")
	case err != nil:
		return fmt.Errorf("etw: starting the session: %w", err)
	}

	r, _, _ := procEnableTraceEx2.Call(uintptr(handle), uintptr(unsafe.Pointer(&kernelDisk)),
		eventControlCodeEnableProvider, traceLevelInformation, ^uintptr(0), 0, 0, 0)
	if r != 0 {
		stopSession()
		return fmt.Errorf("etw: enabling Microsoft-Windows-Kernel-Disk: %w", windows.Errno(r))
	}
	return nil
}

// sessionProps describes the session: real time only, with no file.
func sessionProps() *sessionProperties {
	p := newProperties()
	p.LogFileNameOffset = 0
	p.LogFileMode = eventTraceRealTimeMode
	p.FlushTimer = 1       // seconds, the least it takes; see flushEvery
	p.BufferSize = 64      // KB
	p.MaximumBuffers = 128 // 8MB, the D script's bufsize
	return p
}

// flushEvery hands the session's buffers to the reader every interval,
// until the context is cancelled or done closes.
func flushEvery(ctx context.Context, interval time.Duration, done <-chan struct{}) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-tick.C:
			controlSession(eventTraceControlFlush)
		}
	}
}

func startTrace(handle *uint64, p *sessionProperties) error {
	r, _, _ := procStartTraceW.Call(uintptr(unsafe.Pointer(handle)),
		uintptr(unsafe.Pointer(&sessionName16[0])), uintptr(unsafe.Pointer(p)))
	if r != 0 {
		return windows.Errno(r)
	}
	return nil
}

// controlSession stops or queries the session, by name.
func controlSession(code uint32) (*sessionProperties, error) {
	p := newProperties()
	r, _, _ := procControlTraceW.Call(0, uintptr(unsafe.Pointer(&sessionName16[0])),
		uintptr(unsafe.Pointer(p)), uintptr(code))
	if r != 0 {
		return nil, windows.Errno(r)
	}
	return p, nil
}

func stopSession() { controlSession(eventTraceControlStop) }

// openTrace attaches to the session as its consumer.  The logfile comes
// back to be kept alive for as long as ProcessTrace runs.
func openTrace() (uint64, *eventTraceLogfile, error) {
	lf := &eventTraceLogfile{
		LoggerName:          &sessionName16[0],
		ProcessTraceMode:    processTraceModeRealTime | processTraceModeEventRecord,
		EventRecordCallback: etwCallback,
	}
	r, _, err := procOpenTraceW.Call(uintptr(unsafe.Pointer(lf)))
	if r == invalidProcessTraceHandle {
		return 0, nil, fmt.Errorf("etw: opening the session: %w", err)
	}
	return uint64(r), lf, nil
}

// processTrace delivers events to the callback until the session stops.
func processTrace(trace uint64) error {
	r, _, _ := procProcessTrace.Call(uintptr(unsafe.Pointer(&trace)), 1, 0, 0)
	if r != 0 && windows.Errno(r) != windows.ERROR_CANCELLED {
		return windows.Errno(r)
	}
	return nil
}

func closeTrace(trace uint64) { procCloseTrace.Call(uintptr(trace)) }

// wnodeHeader is WNODE_HEADER.
type wnodeHeader struct {
	BufferSize        uint32
	ProviderID        uint32
	HistoricalContext uint64
	TimeStamp         int64
	GUID              windows.GUID
	ClientContext     uint32
	Flags             uint32
}

// eventTraceProperties is EVENT_TRACE_PROPERTIES.
type eventTraceProperties struct {
	Wnode               wnodeHeader
	BufferSize          uint32
	MinimumBuffers      uint32
	MaximumBuffers      uint32
	MaximumFileSize     uint32
	LogFileMode         uint32
	FlushTimer          uint32
	EnableFlags         uint32
	AgeLimit            int32
	NumberOfBuffers     uint32
	FreeBuffers         uint32
	EventsLost          uint32
	BuffersWritten      uint32
	LogBuffersLost      uint32
	RealTimeBuffersLost uint32
	LoggerThreadID      windows.Handle
	LogFileNameOffset   uint32
	LoggerNameOffset    uint32
}

// sessionProperties is an EVENT_TRACE_PROPERTIES with the room behind it
// where ETW expects to find, or to write back, the session and file names.
type sessionProperties struct {
	eventTraceProperties
	loggerName  [1024]uint16
	logFileName [1024]uint16
}

func newProperties() *sessionProperties {
	p := &sessionProperties{}
	p.Wnode.BufferSize = uint32(unsafe.Sizeof(*p))
	p.Wnode.Flags = wnodeFlagTracedGUID
	p.LoggerNameOffset = uint32(unsafe.Offsetof(p.loggerName))
	p.LogFileNameOffset = uint32(unsafe.Offsetof(p.logFileName))
	return p
}

// eventTraceLogfile is EVENT_TRACE_LOGFILEW, with the two structures a
// real-time consumer never looks at left as bytes.
type eventTraceLogfile struct {
	LogFileName         *uint16
	LoggerName          *uint16
	CurrentTime         int64
	BuffersRead         uint32
	ProcessTraceMode    uint32
	CurrentEvent        [88]byte  // EVENT_TRACE
	LogfileHeader       [280]byte // TRACE_LOGFILE_HEADER
	BufferCallback      uintptr
	BufferSize          uint32
	Filled              uint32
	EventsLost          uint32
	EventRecordCallback uintptr
	IsKernelTrace       uint32
	Context             uintptr
}

// eventRecord is EVENT_RECORD, which is what the callback is handed.
type eventRecord struct {
	EventHeader       eventHeader
	BufferContext     uint32
	ExtendedDataCount uint16
	UserDataLength    uint16
	ExtendedData      uintptr
	UserData          unsafe.Pointer
	UserContext       uintptr
}

// eventHeader is EVENT_HEADER.
type eventHeader struct {
	Size            uint16
	HeaderType      uint16
	Flags           uint16
	EventProperty   uint16
	ThreadID        uint32
	ProcessID       uint32
	TimeStamp       int64
	ProviderID      windows.GUID
	EventDescriptor eventDescriptor
	ProcessorTime   uint64
	ActivityID      windows.GUID
}

// eventDescriptor is EVENT_DESCRIPTOR.
type eventDescriptor struct {
	ID      uint16
	Version uint8
	Channel uint8
	Level   uint8
	Opcode  uint8
	Task    uint16
	Keyword uint64
}
