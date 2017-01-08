package main

import (
    "fmt"
	"strings"
	
	"sort"
	    
    "rexlib/hostinfo"
)

const (
	baseDiskFormat = "%-12s %-4s %-6s %-16v "
)

func getDiskType(di *hostinfo.HIDiskInfo) string {
	switch di.Type {
		case hostinfo.HIDTDisk: 
			return "disk"
		case hostinfo.HIDTVolume: 
			return "vol"
		case hostinfo.HIDTPartition: 
			return "part"
		case hostinfo.HIDTPool: 
			return "pool"
	}
	
	return ""
}

func printDiskField(field, value string) {
	if len(value) == 0 {
		return
	}
	
	fmt.Printf("\t%-12s: %s\n", field, value)
}

func printDiskInfo(nexus *hostinfo.HIObject) {
	fmt.Printf(baseDiskFormat, "NAME", "TYPE", "BUS", "SIZE")
	fmt.Println("PATHS")
	
	// Gather disk names and sort them
	var disks []string
	for name, _ := range nexus.Children {
		disks = append(disks, name)
	}
	sort.Strings(disks)
	
	// Dump disk information
	for _, name := range disks {
		obj := nexus.Children[name]
		di := obj.Object.(*hostinfo.HIDiskInfo)
		
		fmt.Printf(baseDiskFormat, name, getDiskType(di), 
				di.BusType, di.Size)
		first := true
		for _, path := range di.Paths {
			if first {
				first = false
			} else {
				fmt.Printf(baseDiskFormat, "", "", "", "")
			}
			
			fmt.Println(path)
		}
		
		if *verboseFlag {
			printDiskField("Port", di.Port)
			printDiskField("Model", di.Model)
			printDiskField("Identifier", di.Identifier)
			printDiskField("WWN", di.WWN)
		}
		
		// Children names
		var children []string
		for name, _ := range obj.Children {
			children = append(children, name)
		}
		printDiskField("Children", strings.Join(children, " "))
	}
}