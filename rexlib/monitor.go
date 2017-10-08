package rexlib

import (
	"fmt"

	"net"
	"net/rpc"
	"net/url"

	"os"
	"os/exec"

	"sync"

	"path/filepath"

	"tsfile"

	"time"
)

const (
	deferredMonitorDisconnectDelay time.Duration = 10 * time.Minute

	socketRedirectionTimeout     time.Duration = 500 * time.Millisecond
	socketRedirectionTimeoutStep time.Duration = 25 * time.Millisecond

	sockDirectoryPermissions = 0700

	importerEventBatchSize = 32
)

type RexHost struct {
	// Url for connecting for tracing daemon. Could be
	*url.URL

	// SSH process used for forwarding unix socket (wherever applicable)
	sockPath string
	sockCat  *exec.Cmd

	// Client used for access to remote tracing daemon
	client *rpc.Client

	// Connection closer channel (if no activity in 1 minute, connection is dropped)
	connCloser *closerWatchdog
}

type RexMonitoringState struct {
	mu sync.RWMutex

	hosts map[string]*RexHost

	// Name of the user for forwarding unix sockets and their SSH private key
	UserName string
	KeyPath  string

	// Local path where we'll store forwarded sockets
	SocketDirectory string
}

type IncidentEventArgs struct {
	// Input arguments: name of incident and page tag for series
	Incident string
	Tag      tsfile.TSFPageTag

	// Entries range to retrieve
	Start int
	Count int
}

type IncidentEventReply struct {
	Schema *tsfile.TSFSchemaHeader
	Data   [][]byte
}

var monState *RexMonitoringState

// Checks if current daemon works in monitor mode
func IsMonitorMode() bool {
	return monState != nil
}

// Initializes rex-mon state with username/their keypath and list of host URLs
func InitializeMonitor(userName, keyPath, sockDir string, hosts []*url.URL) error {
	if _, err := os.Stat(sockDir); os.IsNotExist(err) {
		err = os.Mkdir(sockDir, sockDirectoryPermissions)
		if err != nil {
			return err
		}
	}

	monState = &RexMonitoringState{
		UserName:        userName,
		KeyPath:         keyPath,
		SocketDirectory: sockDir,

		hosts: make(map[string]*RexHost),
	}

	for _, hostUrl := range hosts {
		monState.hosts[hostUrl.Host] = &RexHost{
			URL: hostUrl,
		}
	}

	return nil
}

func (state *RexMonitoringState) getHost(hostName string) (*RexHost, error) {
	if host, ok := monState.hosts[hostName]; ok {
		return host, nil
	}

	return nil, fmt.Errorf("Host '%s' cannot be found", hostName)
}

func (host *RexHost) connectUnixRexSocket() (conn *net.UnixConn, err error) {
	if len(host.sockPath) == 0 {
		host.sockPath = filepath.Join(monState.SocketDirectory,
			fmt.Sprintf("%s.sock", host.URL.Host))
		host.sockCat = exec.Command("ssh", "-NT", "-l", monState.UserName, "-i",
			monState.KeyPath, "-L", fmt.Sprintf("%s:%s", host.sockPath,
				host.URL.Path), host.URL.Host)
		host.sockCat.Start()

		// Wait for socket is being redirected or timeout will expire
		timer := time.NewTimer(socketRedirectionTimeout)
		ticker := time.NewTicker(socketRedirectionTimeoutStep)
		defer timer.Stop()
		defer ticker.Stop()

		var stat os.FileInfo
		for stat == nil || stat.Mode()&os.ModeSocket == 0 {
			select {
			case <-timer.C:
				// Timeout is expired, probably ssh is failed, wait for it...
				return nil, host.closeUnixRexSocket()
			case <-ticker.C:
				stat, _ = os.Stat(host.sockPath)
			}
		}
	}

	addr, err := net.ResolveUnixAddr("unix", host.sockPath)
	if err != nil {
		return nil, err
	}

	return net.DialUnix("unix", nil, addr)
}

func (host *RexHost) closeUnixRexSocket() (err error) {
	if host.sockCat != nil {
		host.sockCat.Process.Kill()
		err = host.sockCat.Wait()
	}

	if len(host.sockPath) > 0 {
		if _, statErr := os.Stat(host.sockPath); statErr == nil {
			err = os.Remove(host.sockPath)
		}
		host.sockPath = ""
	}

	if err == nil && host.sockCat.ProcessState != nil {
		return fmt.Errorf("ssh redirector has failed, %s",
			host.sockCat.ProcessState.String())
	}

	return
}

func Connect(hostName string) (*rpc.Client, error) {
	monState.mu.Lock()
	defer monState.mu.Unlock()

	host, err := monState.getHost(hostName)
	if err != nil {
		return nil, err
	}

	if host.connCloser == nil {
		host.connCloser = newCloserWatchdog(deferredMonitorDisconnectDelay)
	}
	if host.client != nil {
		host.connCloser.Notify()
		return host.client, nil
	}

	// Forward Unix socket from remote host
	if host.URL.Scheme == "unix" {
		conn, err := host.connectUnixRexSocket()
		if err != nil {
			return nil, err
		}

		host.client = rpc.NewClient(conn)
	} else {
		return nil, fmt.Errorf("Invalid host scheme '%s', only 'unix' is supported",
			host.URL.Scheme)
	}

	go func() {
		host.connCloser.Wait()
		host.closeUnixRexSocket()
	}()

	return host.client, nil
}

func DisconnectAll() {
	monState.mu.Lock()
	defer monState.mu.Unlock()

	for _, host := range monState.hosts {
		if host.sockCat != nil {
			host.closeUnixRexSocket()
		}
	}
}

func GetMonitoredHosts() (hosts []string) {
	monState.mu.RLock()
	defer monState.mu.RUnlock()

	hosts = make([]string, 0, len(monState.hosts))
	for host, _ := range monState.hosts {
		hosts = append(hosts, host)
	}
	return
}

func ImportIncident(hostName, incidentName string) (incident *Incident, err error) {
	clnt, err := Connect(hostName)
	if err != nil {
		return
	}

	other := new(Incident)
	err = clnt.Call("SRVRex.GetIncident", incidentName, other)
	if err != nil {
		return
	}

	local, err := Incidents.New(other)
	if err != nil {
		return
	}

	handle, err := local.createHandle()
	if err != nil {
		return
	}

	go handle.runImporter()
	return local, nil
}

func (handle *IncidentHandle) runImporter() {
	incident := handle.incident
	ilog := handle.providerOutput.Log

	// Save incident
	ilog.Println("Importing incident from", incident.Host)
	err := incident.saveBoth()
	if err != nil {
		ilog.Println(err)
	}

	defer handle.Close()

	importing := true
	for importing {
		importing = (handle.updateMonitoredIncident() != IncStopped)
		if handle.client == nil {
			break
		}

		// Compare local and remote stats and get all new enties
		for index, seriesStats := range incident.TraceStats.Series {
			if seriesStats.Count == 0 {
				continue
			}

			err := handle.importMonitoredSeries(index, seriesStats)
			if err != nil {
				ilog.Printf("Cannot import series %s: %v", seriesStats.Name, err)
			}
		}

		incident.TraceStats = handle.providerOutput.Trace.GetStats()
		incident.save()

		// No need for precise ticks in importer
		time.Sleep(time.Duration(incident.TickInterval) * time.Millisecond)
	}

	incident.save()
}

func (handle *IncidentHandle) updateMonitoredIncident() IncState {
	incident := handle.incident
	incident.mtx.Lock()
	defer incident.mtx.Unlock()

	// Always refresh client instance in case we hit closed connection
	handle.client = nil
	clnt, err := Connect(incident.Host)
	if err != nil {
		handle.providerOutput.Log.Println(err)

		incident.StoppedAt = time.Now()
		return IncStopped
	}

	err = clnt.Call("SRVRex.GetIncident", incident.Name, incident)
	if err != nil {
		handle.providerOutput.Log.Println(err)
	} else {
		handle.client = clnt
	}

	return incident.getStateNoLock()
}

func (handle *IncidentHandle) importMonitoredSeries(index int,
	seriesStats tsfile.TSFSeriesStats) (err error) {

	args := IncidentEventArgs{
		Incident: handle.incident.Name,
		Tag:      seriesStats.Tag,
	}

	trace := handle.providerOutput.Trace

	// Check if we already imported entries for this series
	count := trace.GetEntryCount(args.Tag)
	if count > 0 {
		args.Start = count
	}

	for uint(args.Start) < seriesStats.Count {
		// Limit amount of entries retrieved
		args.Count = int(seriesStats.Count) - args.Start
		if args.Count > importerEventBatchSize {
			args.Count = importerEventBatchSize
		}

		// Retrieve events from tracer
		var reply IncidentEventReply
		err = handle.client.Call("SRVRex.GetEvents", &args, &reply)
		if err != nil {
			return
		}

		if args.Start == 0 {
			// We expect that seriesStats are sorted according to the order
			// of page tags and tsfile will allocate them sequentally too, so
			// local series for say "sysstat" provider will get same page tag
			// as on remote tracer
			tag, err := trace.AddSchema(reply.Schema)
			if err != nil {
				return err
			}
			if tag != args.Tag {
				return fmt.Errorf("unexpected tag id %d", tag)
			}
		}

		trace.AddEntries(args.Tag, reply.Data)
		args.Start += len(reply.Data)
	}

	return nil
}
