package hostinfo

import (
	"os"
	"path/filepath"
	
	"fmt"
	"strings"
	
	"regexp"
)

const (
	sysBlockPath = "/sys/block"
	sysScsiHostPath = "/sys/class/scsi_host"
	devDiskPath = "/dev/disk"
	devPath = "/dev"
	
	defaultSectorSize = 512
	
	diskMaximumPartitions = 256 // DISK_MAX_PARTS
)

var devPathDirs []string = []string{
	"/dev/disk/by-id",
	"/dev/disk/by-label",
	"/dev/disk/by-path",
	"/dev/disk/by-uuid",
	"/dev/md",
	"/dev/mapper",
}

var (
	reDiskBus = regexp.MustCompile("^(s|h|v|xv|dm|ram|md|loop)")
)

// Probes disks on linux
func (di *HIDiskInfo) Probe(nexus *HIObject) (err error) {
	err = di.procDisks(nexus)
	if err != nil {
		return
	}
	
	// Process slaves
	for _, diObj := range nexus.Children {
		diObj.procSlaves(nexus)
	}
	
	// Resolve extra paths generated by udev, etc. (symlinks)
	// TODO: extend with /dev/VGNAME for LVM?
	for _, devPathDir := range devPathDirs {
		filepath.Walk(devPathDir, func(path string, info os.FileInfo, err error) error {
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			
			// Find out original object and add path to it
			name := filepath.Base(realPath)
			if diObj, ok := nexus.Children[name]; ok {
				di := diObj.Object.(*HIDiskInfo)
				di.Paths = append(di.Paths, path)
			}
			
			return nil
		})
	}
	
	// TODO: LVM, btrfs...
	
	return 
}

func (di *HIDiskInfo) procDisks(nexus *HIObject) (err error) {
	walker, err := sysfsOpen(sysBlockPath)
	if err != nil {
		return
	}
	
	for walker.Next() {
		name := walker.GetName()
		
		// TODO: port hi_linux_make_dev_path (replaces ! or . with '/')
		diskPath := filepath.Join(devPath, name)
		
		diskInfo := &HIDiskInfo {
			Name: name,
			Paths: []string{diskPath},
		}
		
		sectorSize := walker.ReadInt("queue/hw_sector_size", 10, 32, defaultSectorSize)
		diskInfo.Size = walker.ReadInt("size", 10, 64, 0) * sectorSize
		diskInfo.BusType = reDiskBus.FindString(name)
		
		trace(HITraceDisk, "Processing disk %s (%s): bus=%s sz=%d", name, diskPath, 
					diskInfo.BusType, diskInfo.Size)
		
		havePartitions := false
		switch diskInfo.BusType {
			// DeviceMapper
			case "dm":
				diskInfo.Type = HIDTVolume
				diskInfo.Identifier = walker.ReadLine("dm/uuid")
			// RAM & LOOP devices
			case "ram", "loop":
				diskInfo.Type = HIDTVolume
			// MD-RAID
			case "md":
				diskInfo.Type = HIDTVolume
				havePartitions = true
			default:
				diskInfo.Type = HIDTDisk
				diskInfo.Model = walker.ReadLine("device/model")
				diskInfo.Port = filepath.Base(walker.ReadLink("device"))
				
				switch diskInfo.BusType {
					case "h":
						diskInfo.BusType = "ide"
					case "s":
						diskInfo.BusType = diskInfo.getBus()
					case "v":
						diskInfo.BusType = "virtio"
					case "xv":
						diskInfo.BusType = "xen"
				}
				
				havePartitions = true
		}
		
		// TODO: WWN
		
		diObj := nexus.Attach(name, diskInfo)
		if havePartitions {
			diskInfo.procPartitions(nexus, diObj, sectorSize)
		}
	}
	
	return
}

func (di *HIDiskInfo) getBus() string {
	// Parse port id and find corresponding scsi host
	idx := strings.IndexRune(di.Port, ':')
	if idx == -1 {
		return "" 
	}
	
	host := fmt.Sprintf("host%s", di.Port[0:idx])
	walker, err := sysfsOpenNode(sysScsiHostPath, host)
	if err != nil {
		trace(HITraceDisk, "Cannot check bus for disk %s, host %s: %v", di.Name, host, err)
		return ""
	}
	
	bus := walker.ReadLine("proc_name")
	trace(HITraceDisk, "Bus for disk %s, host %s is %s", di.Name, host, bus)
	
	return bus 
}

func (di *HIDiskInfo) procPartitions(nexus, diObj *HIObject, sectorSize int64) {
	walker, err := sysfsOpen(filepath.Join(sysBlockPath, di.Name))
	if err != nil {
		trace(HITraceDisk, "Cannot process partitions for disk %s: %v", di.Name, err)
		return
	}
	
	for walker.Next() {
		if !walker.GetFileInfo().IsDir() {
			continue
		}
		
		name := walker.GetName()
		if !strings.HasPrefix(name, di.Name) {
			continue
		}
		
		partId := walker.ReadInt("partition", 10, 8, diskMaximumPartitions+1)
		if partId < 0 || partId >= diskMaximumPartitions {
			trace(HITraceDisk, "Invalid partition #%d for subdir %s", partId, name)
			continue
		}
		
		// Create partition -- it is added both to subdisk and as root-visible device
		partPath := filepath.Join(devPath, walker.GetName()) 
		
		partInfo := &HIDiskInfo {
			Name: name,
			Paths: []string{partPath},
			Type: HIDTPartition,
		}
		partInfo.Size = walker.ReadInt("size", 10, 64, 0) * sectorSize		
		
		nexus.Attach(name, partInfo)
		diObj.Attach(name, partInfo)
	}
}

// Processes slaves for an disk if those are exist
func (diObj *HIObject) procSlaves(nexus *HIObject) {
	di := diObj.Object.(*HIDiskInfo)
	walker, err := sysfsOpen(filepath.Join(sysBlockPath, di.Name, "slaves"))
	if err != nil {
		return
	}
	
	for walker.Next() {
		name := walker.GetName()
		if slaveObj, ok := nexus.Children[name]; ok {
			// Add link slave <-> parent obj
			slaveObj.Parent = diObj
			diObj.Children[name] = slaveObj
			
			trace(HITraceDisk, "Found slave %s for parent %s", name, di.Name)
		
			// Resolve mapper pools
			slaveDi := slaveObj.Object.(*HIDiskInfo)
			if slaveDi.Type == HIDTDisk && di.Type == HIDTVolume {
				di.Type = HIDTPool
			}
		}
	}
}
