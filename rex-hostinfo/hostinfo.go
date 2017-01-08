package main 

import (
    "flag"
    
	"log"
	"fmt"
    
    "rexlib/hostinfo"
)

// REXHostInfo -- tool to dump host information 

var verboseFlag *bool = flag.Bool("v", false, "Print extra information")
var tracingFlag *bool = flag.Bool("T", false, "Enable HostInfo tracing")
var noHeaderFlag *bool = flag.Bool("H", false, "Do not show headers")

func main() {	
    flag.Parse()
    if *tracingFlag {
    	hostinfo.TracingFlags = 0xFFFF
    }
	
	subsystems := flag.Args()
	if len(subsystems) == 0 {
		subsystems = []string{"proc", "disk"};
	}
	
	for index, subsys := range subsystems {
		if index > 0 {
			// Add empty line between subsystems
			fmt.Println("")
		}
		
		switch subsys {
			case "proc":
				nexus, err := hostinfo.GetNexus(hostinfo.HIProc, false)
				if err != nil {
					log.Fatalf("Error in proc subsystem: %v\n", err)
				}
				
				printProcInfo(nexus)
			case "disk":
				nexus, err := hostinfo.GetNexus(hostinfo.HIDisk, false)
				if err != nil {
					log.Fatalf("Error in disk subsystem: %v\n", err)
				}
				
				printDiskInfo(nexus)
			default:
				log.Fatalf("Invalid hostinfo subsystem: %s", subsys)
		}		
	}
}

