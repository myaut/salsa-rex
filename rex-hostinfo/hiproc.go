package main

import (
	"fmt"
	
    "rexlib/hostinfo"
)

const (
	procFormat = "%-12s %-6v %-6v %-16s\n"
)

func printProcInfo(nexus *hostinfo.HIObject) {
	fmt.Printf(procFormat, "USER", "PID", "PPID", "CMDLINE")
	
	for pidStr, procObj := range nexus.Children {
		pi := procObj.Object.(*hostinfo.HIProcInfo)
		
		fmt.Printf(procFormat, pi.User, pidStr, pi.PPID, pi.CommandLine)
	}
}
