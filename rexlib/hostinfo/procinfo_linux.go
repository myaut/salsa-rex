package hostinfo

import (
	"os"
	"io/ioutil"
	"path/filepath"
	
	"os/user"
	
	"strings"	
	"strconv"
	
	"io"	
	"bufio"
)

const (
	procPath = "/proc"
)

// Gets process table on Linux
func (rootPi *HIProcInfo) Probe(nexus *HIObject) error {
	procs, err := ioutil.ReadDir(procPath)
	if err != nil {
		return err
	}
	
	for _, proc := range procs {
		// TODO: Grouping support
		
		// Check if it is directory with numeric name. If not its OK, 
		// procfs have many different things...
		if !proc.IsDir() {
			continue
		}
		
		pidStr := proc.Name()
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		
		pi := new(HIProcInfo)
		err = pi.readStatus(pidStr)
		if err != nil {
			trace(HITraceProc, "Error reading status file for PID %d: %v", pid, err)
			continue 
		}
		
		pi.readCommandLine(pidStr)
		
		nexus.Attach(pidStr, pi)
	}
	
	return nil
}

// Reads status file in format key:\s+value
func (pi *HIProcInfo) readStatus(pidStr string) error {
	file, err := os.Open(filepath.Join(procPath, pidStr, "status"))
	if err != nil {
		return err
	}
	
	buf := bufio.NewReader(file)
	for {
		key, err := buf.ReadString(':')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		
		value, err := buf.ReadString('\n')
		if err != nil {
			return err
		} 
		
		key = strings.TrimSuffix(key, ":")
		value = strings.TrimSpace(value)
		
		switch key {
			case "Name":
				pi.ExecName = value
				pi.CommandLine = value
			case "PPid":
				ppid, _ := strconv.ParseUint(value, 10, 32)
				pi.PPID = uint32(ppid)
			case "Pid":
				pid, _ := strconv.ParseUint(value, 10, 32)
				pi.PID = uint32(pid)
			case "Uid":
				uids := strings.SplitAfter(value, "\t")
				uidStr := strings.TrimSpace(uids[0])
				u, err := user.LookupId(uidStr)
				
				if err == nil {
					pi.User = u.Username
				} else {
					pi.User = uidStr				
				}	
		} 
	}
}

// Reads cmdline file
func (pi *HIProcInfo) readCommandLine(pidStr string) {
	file, err := os.Open(filepath.Join(procPath, pidStr, "cmdline"))
	if err != nil {
		return
	}
	
	var args []string
	buf := bufio.NewReader(file)
	for {
		arg, err := buf.ReadString('\000')
		if err != nil {
			return
		}
		
		// TODO: escape
		args = append(args, arg)
	}
	
	pi.CommandLine = strings.Join(args, " ")
}

