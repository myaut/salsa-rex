package hostinfo

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"strconv"
	"strings"

	"bufio"
	"fmt"
	"io"

	"time"

	"rexlib/hostinfo/syscall"
)

const (
	procPath = "/proc"
)

var jiffiesPerSecond uint64 = syscall.GetClkTck()
var jiffiesInSecond time.Duration = time.Second / time.Duration(jiffiesPerSecond)

func jiffiesToDuration(t uint64) time.Duration {
	return time.Duration(t/jiffiesPerSecond)*time.Second +
		time.Duration(t%jiffiesPerSecond)*jiffiesInSecond
}

// Helper for reading status-like files in format key : value
type ProcFileReader struct {
	reader    *bufio.Reader
	lastError error
}

func (pfr *ProcFileReader) setLastError(err error) {
	if pfr.lastError == nil && err != io.EOF {
		pfr.lastError = err
	}

	trace(HITraceProc, "Error in ProcFileReader: %v", err)
}

func (pfr *ProcFileReader) ReadString(expectedKey string) string {
	for {
		key, err := pfr.reader.ReadString(':')
		if err != nil {
			pfr.setLastError(err)
			return ""
		}

		value, err := pfr.reader.ReadString('\n')
		if err != nil {
			pfr.setLastError(err)
			return ""
		}

		key = strings.TrimSuffix(key, ":")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if key == expectedKey {
			return value
		}
	}
}

func (pfr *ProcFileReader) ReadInteger(expectedKey string, base, bitSize int) uint64 {
	value := pfr.ReadString(expectedKey)
	if pfr.lastError != nil {
		return 0
	}

	ival, err := strconv.ParseUint(value, base, bitSize)
	if err != nil {
		pfr.setLastError(err)
	}
	return ival
}

func (pfr *ProcFileReader) ReadMemory(expectedKey string, expectedUnit string,
	ratio uint64) uint64 {
	value := pfr.ReadString(expectedKey)
	if pfr.lastError != nil {
		return 0
	}

	tokens := strings.SplitN(value, " ", 2)
	if len(tokens) != 2 || tokens[1] != expectedUnit {
		pfr.setLastError(fmt.Errorf("Unexpected memory unit for key %s / value '%s'",
			expectedKey, value))
		return 0
	}

	ival, err := strconv.ParseUint(tokens[0], 10, 64)
	if err != nil {
		pfr.setLastError(err)
	}
	return ratio * ival
}

// Gets process table on Linux
type HIProcessProber struct {
	kernelThreads map[uint32]bool
}

func (prober *HIProcessProber) Probe(nexus *HIObject) error {
	prober.kernelThreads = make(map[uint32]bool)

	procs, err := ioutil.ReadDir(procPath)
	if err != nil {
		trace(HITraceProc, "Error reading procfs dir %s: %v", procPath, err)
		return err
	}

	for _, proc := range procs {
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
		basePath := filepath.Join(procPath, pidStr)

		err = prober.readStatus(basePath, pi)
		if err != nil {
			trace(HITraceProc, "Error reading status file for PID %d: %v", pid, err)
			continue
		}

		pi.readCommandLine(basePath)
		pi.readIOStat(basePath)

		obj := nexus.Attach(pidStr, pi)
		prober.readTaskList(basePath, obj)
	}

	return nil
}

// Reads status file in format key:\s+value
func (prober *HIProcessProber) readStatus(basePath string, pi *HIProcInfo) error {
	file, err := os.Open(filepath.Join(basePath, "status"))
	if err != nil {
		return err
	}

	pfr := ProcFileReader{reader: bufio.NewReader(file)}

	pi.ExecName = pfr.ReadString("Name")
	pi.PID = uint32(pfr.ReadInteger("Pid", 10, 32))
	pi.PPID = uint32(pfr.ReadInteger("PPid", 10, 32))

	// Check out for kernel threads
	if pi.PPID == 0 || prober.kernelThreads[pi.PPID] {
		prober.kernelThreads[pi.PID] = true
		pi.KernelThread = true
		return pfr.lastError
	}

	// Read list of uids, while we're only interested in first one
	uids := strings.SplitAfter(pfr.ReadString("Uid"), "\t")
	uidStr := strings.TrimSpace(uids[0])

	uid, _ := strconv.ParseUint(uidStr, 10, 32)
	pi.UID = uint32(uid)

	// Read memory stats
	pi.VSZ = pfr.ReadMemory("VmSize", "kB", 1024)
	pi.RSS = pfr.ReadMemory("VmRSS", "kB", 1024)

	return pfr.lastError
}

func (pi *HIProcInfo) readIOStat(basePath string) {
	file, err := os.Open(filepath.Join(basePath, "io"))
	if err != nil {
		return
	}

	pfr := ProcFileReader{reader: bufio.NewReader(file)}

	pi.RChar = pfr.ReadInteger("rchar", 10, 64)
	pi.WChar = pfr.ReadInteger("wchar", 10, 64)
}

// Reads list of threads associated with this process
func (prober *HIProcessProber) readTaskList(basePath string, obj *HIObject) {
	basePath = filepath.Join(basePath, "task")
	taskDirs, err := ioutil.ReadDir(basePath)
	if err != nil {
		return
	}

	// Assuming reading is fast enough, read and cache uptime
	uptime := prober.readUptime()

	for _, taskDir := range taskDirs {
		tidStr := taskDir.Name()
		tid, _ := strconv.Atoi(tidStr)

		taskPath := filepath.Join(basePath, tidStr)
		file, err := os.Open(filepath.Join(taskPath, "status"))
		if err != nil {
			trace(HITraceProc, "Error reading %s/status: %v", taskPath, err)
			continue
		}

		tfr := ProcFileReader{reader: bufio.NewReader(file)}

		ti := new(HIThreadInfo)
		ti.TID = uint32(tid)
		ti.Name = tfr.ReadString("Name")
		ti.VCS = tfr.ReadInteger("voluntary_ctxt_switches", 10, 64)
		ti.IVCS = tfr.ReadInteger("nonvoluntary_ctxt_switches", 10, 64)

		file, err = os.Open(filepath.Join(taskPath, "stat"))
		if err != nil {
			continue
		}

		// Read stat file and discard thread id and name as we already know it
		// from directory name and status file (also saves us parsing name)
		buf := bufio.NewReader(file)
		buf.Discard(len(tidStr) + len(" (") + len(ti.Name) + len(")"))

		var startTime, utime, stime uint64
		var i64 int64
		var ui64 uint64

		_, err = fmt.Fscanf(buf, " %c", &ti.State)
		if err != nil {
			trace(HITraceProc, "Error in Fscanf(%s/stat:state): %v", taskPath, err)
			continue
		}

		n, err := fmt.Fscan(buf,
			// ppid  pgrp  sess  tty#  tpgid flags
			&i64, &i64, &i64, &i64, &i64, &ui64,
			// minflt  	  c* 	majflt  	   c*	  utime   stime
			&ti.MinFault, &ui64, &ti.MajFault, &ui64, &utime, &stime,
			// c[us]time  prio  nice  nt    itrv  starttime
			&ui64, &ui64, &i64, &i64, &i64, &i64, &startTime,
		)
		if err != nil {
			trace(HITraceProc, "Error in Fscan(%s/stat): %v (after %d fields)", taskPath, err, n)
			continue
		}

		threadLifetime := jiffiesToDuration(startTime)
		ti.Lifetime = uptime - threadLifetime
		ti.STime = jiffiesToDuration(stime)
		ti.UTime = jiffiesToDuration(utime)

		obj.Attach(tidStr, ti)
	}
}

func (prober *HIProcessProber) readUptime() time.Duration {
	var uptimeF float64

	file, err := os.Open("/proc/uptime")
	if err == nil {
		_, err = fmt.Fscan(file, &uptimeF)
	}
	if err != nil {
		trace(HITraceProc, "Error reading /proc/uptime: %v", err)
		return time.Second
	}

	return time.Duration(uptimeF * float64(time.Second))
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
