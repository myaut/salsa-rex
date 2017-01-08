package hostinfo

import (
	"fmt"
	"log"
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

type HIObjectImpl interface {
	// Probes nexus node and fills up children 
	Probe(nexus *HIObject) error
}

// Nexus node
type hiSubSys struct { 
	Id int 
	
	Nexus HIObject
	
	state int
}

// HostInfo object which creates a tree of hostinfo
type HIObject struct {
	// Tree relationships
	Parent *HIObject
	Children map[string]*HIObject
	
	// Actual node (it is not filled in nexus node)
	Object HIObjectImpl 
}

// Subsystems list
var subsystems []hiSubSys = []hiSubSys{
	hiSubSys{Id: HIProc},
	hiSubSys{Id: HIDisk},
}

// Probes subsystem (if necessary) and returns nexus node. If devices
// already probed and reprobe is set to false, returns previous list
func GetNexus(id int, reprobe bool) (*HIObject, error) {
	if id >= len(subsystems) {
		return nil, fmt.Errorf("Unknown subsystem #%d", id) 
	}
	
	// TODO: locking
	subsys := &subsystems[id]
	if subsys.state == hissError {
		return nil, fmt.Errorf("Subsystem probing have failed last time") 
	}
	
	nexus := &subsys.Nexus
	if subsys.state == hissNotProbed || reprobe {
		// Initialize nexus node and children
		subsys.Nexus.Children = make(map[string]*HIObject)
		switch id {
			case HIProc:
				subsys.Nexus.Object = new(HIProcInfo)
			case HIDisk:
				subsys.Nexus.Object = new(HIDiskInfo)
		}
		
		err := subsys.Nexus.Object.Probe(nexus)
		if err != nil {
			subsys.state = hissError
			return nil, err			
		}
		
		subsys.state = hissOK
	}
	
	// hissOK
	return nexus, nil
}

// Insert root device into parent
func (nexus *HIObject) Attach(name string, info HIObjectImpl) *HIObject {
	obj := &HIObject {
		Parent: nexus,
		Children: make(map[string]*HIObject),
		Object: info,
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
	// TODO: Group string
	
	// PID as string is used as child name
	PID uint32
	PPID uint32
	
	ExecName string
	CommandLine string
	
	// Name of UID which is running it
	User string
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
	Port string
	
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

var TracingFlags = 0

func trace(subsys int, format string, v ...interface{}) {
	if (TracingFlags & subsys) == subsys {
		log.Printf(format, v...)
	}
}
