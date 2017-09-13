package hostinfo

import (
	"fmt"
	"log"

	"os"

	"sync"
	"time"

	"strings"
)

// Subsystems (which are actually indexes in subsys array)
const (
	HIProc = iota
	HIDisk
)

// Subsystem states
const (
	hissNotProbed = iota
	hissOK
	hissError
)

const collectionInterval time.Duration = 200 * time.Millisecond
const statsCollectionInterval time.Duration = 10 * time.Millisecond

type HIObjectImpl interface {
}

type HISubsystemImpl interface {
	// Probes node and fills up nexus with children
	Probe(nexus *HIObject) error

	// Updates statistics without re-probing data
	UpdateStats(nexus *HIObject) error
}

// Nexus node wrapper
type hiSubSys struct {
	Id int

	Nexus HIObject

	mu   sync.Mutex
	impl HISubsystemImpl

	state               int
	lastCollection      time.Time
	lastStatsCollection time.Time
}

// HostInfo object which creates a tree of hostinfo
type HIObject struct {
	// Tree relationships
	Children map[string]*HIObject

	// Actual node (it is not filled in nexus node)
	Object HIObjectImpl
}

// Subsystems list
var subsystems []hiSubSys = []hiSubSys{
	hiSubSys{Id: HIProc, impl: new(HIProcessProber)},
	hiSubSys{Id: HIDisk, impl: new(HIDiskProber)},
}

// Probes subsystem (if necessary) and returns nexus node. If devices
// already probed and reprobe is set to false, returns previous list
func GetNexus(id int, reprobe bool, stats bool) (*HIObject, error) {
	if id >= len(subsystems) {
		return nil, fmt.Errorf("Unknown subsystem #%d", id)
	}

	subsys := &subsystems[id]
	if subsys.state == hissError {
		return nil, fmt.Errorf("Subsystem probing have failed last time")
	}

	subsys.mu.Lock()
	defer subsys.mu.Unlock()

	nexus := &subsys.Nexus
	if reprobe {
		reprobe = subsys.lastCollection.Before(time.Now().Add(-collectionInterval))
	}
	if subsys.state == hissNotProbed || reprobe {
		// Initialize nexus node and children
		subsys.Nexus.Children = make(map[string]*HIObject)
		err := subsys.impl.Probe(nexus)
		if err != nil {
			subsys.state = hissError
			return nil, err
		}

		subsys.state = hissOK
		subsys.lastCollection = time.Now()
		subsys.lastStatsCollection = subsys.lastCollection
	}
	if stats && subsys.lastStatsCollection.Before(time.Now().Add(-statsCollectionInterval)) {
		subsys.impl.UpdateStats(nexus)
		subsys.lastStatsCollection = time.Now()
	}

	// hissOK
	return nexus, nil
}

// Insert root device into parent
func (nexus *HIObject) Attach(name string, info HIObjectImpl) *HIObject {
	obj := &HIObject{
		Children: make(map[string]*HIObject),
		Object:   info,
	}
	nexus.Children[name] = obj

	return obj
}

// ---------------------------
// Subsystem-specific objects

// ProcInfo types -- should only have non-dynamic properties
type HIProcInfo struct {
	// This node is not an actual process, but a group of processes
	// which might be PGID-based, CGroup-based. PPIDs are not considered as
	// groups

	// PID as string is used as child name
	PID  uint32
	PPID uint32

	ExecName    string
	CommandLine string

	UID uint32

	// Kernel threads
	KernelThread bool

	// TODO: support for mapping
	// TODO: support for open files & working/root directories

	// Virtual memory statistics (current)
	VSZ, RSS uint64
	// Allocated/freed bytes physically per second
	VAllocs, PAllocs int64
	// read/write bytes per second
	RChar, WChar int64

	Lifetime time.Duration

	// Actual absolute stats
	rChar, wChar int64
}

type HIThreadInfo struct {
	TID  uint32
	Name string

	State rune

	// Relative stats (per second, since last gather interval)
	VCS, IVCS          int64
	MinFault, MajFault int64
	UTime, STime       time.Duration

	Lifetime time.Duration

	// Absolute stats (since thread start)
	vcs, ivcs          int64
	minFault, majFault int64
	uTime, sTime       time.Duration
}

// DiskInfo types
const (
	// Unknown object (used internally)
	HIDTUnknown = iota

	// Physical disk or a LUN present from storage
	HIDTDisk

	// Partition of the disk
	HIDTPartition

	// Pool which consists of multiple disks and used by Volume Manager.
	// Doesn't have corresponding block device
	HIDTPool

	// Logical volume presented by Volume Manager
	HIDTVolume
)

type HIDiskType int
type HIDiskInfo struct {
	// Type of the disk
	Type HIDiskType

	// Name of the disk (same as used in map)
	Name string

	// Paths and aliases corresponding to this disk
	Paths []string

	// Size of the disk
	Size int64

	// Type of the bus and identification used by port
	BusType string
	Port    string

	// WorldWide Number of a disk
	WWN string

	// Internally used identifier
	Identifier string

	// Vendor and model name
	Model string
}

// Tracing
const (
	HITraceUname = 1 << iota
	HITraceObject
	HITraceHelpers
	HITraceProc
	HITraceDisk
	HITraceCPU
	HITraceNet
	HITraceFS
)

var TracingFlags = getTraceFlags()

func trace(subsys int, format string, v ...interface{}) {
	if (TracingFlags & subsys) == subsys {
		log.Printf(format, v...)
	}
}

func getTraceFlags() (flags int) {
	flagNames := strings.Split(os.Getenv("REX_HI_TRACE"), ",")
	for _, flag := range flagNames {
		switch flag {
		case "uname":
			flags |= HITraceUname
		case "obj":
			flags |= HITraceObject
		case "helpers":
			flags |= HITraceHelpers
		case "proc":
			flags |= HITraceProc
		case "disk":
			flags |= HITraceDisk
		case "cpu":
			flags |= HITraceCPU
		case "net":
			flags |= HITraceNet
		case "fs":
			flags |= HITraceFS
		}
	}
	return
}
