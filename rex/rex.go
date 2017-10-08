package main

import (
	"fmt"
	"log"

	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"

	"flag"

	"net"
	"net/rpc"

	"strconv"

	"fishly"
	"rexlib"

	"github.com/go-ini/ini"
)

//
// rex is a standalone binary which runs on system under test and operates
// in three modes:
//   * rex-t runs as system tracer daemon which listens of RPC ops
//     on Unix socket
//   * rex serves as a CLI to tracer and if tracer if not run, it starts
//     its own, thus providing standalone mode
//   * rex-mon is a monitor which provides higher-level monitoring
//     on top of rex -t
//

type RexConfig struct {
	// Main (REX) configuration variables
	Socket      string
	SocketGroup string

	DataDir    string
	TSLoadPath string

	// CLI-related variables
	cliCfg   fishly.UserConfig
	cliRLCfg fishly.ReadlineConfig

	// Monitor-related variables
	monCfg RexMonConfig
}

type RexMonConfig struct {
	Key    string
	User   string
	Socket string
	Hosts  []string
}

func main() {
	configPath := flag.String("config", "rex.ini", "path to the rex config")
	autoExec := flag.String("exec", "", "command to be automatically executed")
	initContext := flag.String("ctx", "", "initial context state")

	trace := flag.Bool("t", false, "start as a tracing daemon")
	mon := flag.Bool("mon", false, "start as a monitoring daemon")
	flag.Parse()

	var cfg RexConfig
	cfg.load(*mon, *configPath)

	switch {
	case *mon, *trace:
		// TODO daemonize

		cfg.setupRPC(*mon)
		listener := cfg.bindRexSocket()
		defer listener.Close()
		defer cfg.unlinkRexSocket()
		defer rexlib.Shutdown()

		go cfg.serve(listener)
		cfg.waitForExitSignal()
	default:
		if _, err := os.Stat(cfg.Socket); os.IsNotExist(err) {
			cfg.setupRPC(*mon)

			cfg.setUniqueSocketPath()
			listener := cfg.bindRexSocket()
			defer listener.Close()

			go cfg.serveOne(listener)
		}
		defer rexlib.Shutdown()

		log.Printf("Welcome to REX CLI [%d]", os.Getpid())

		var ctx RexContext
		ctx.startCLI(&cfg, *autoExec, *initContext)
	}
}

func (rexCfg *RexConfig) load(isMon bool, path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		log.Fatalf("Config '%s' doesn't exist", path)
	}

	cfg, err := ini.Load(path)
	if err != nil {
		log.Fatalln(err)
	}

	err = cfg.Section("rex").MapTo(&rexCfg)
	if err != nil {
		log.Fatalln(err)
	}

	if isMon {
		cfg.Section("mon").MapTo(&rexCfg.monCfg)
	} else {
		cfg.Section("cli").MapTo(&rexCfg.cliCfg)
		cfg.Section("cli").MapTo(&rexCfg.cliRLCfg)
	}
}

func (rexCfg *RexConfig) setUniqueSocketPath() {
	rexCfg.Socket = fmt.Sprintf("%s.%d", rexCfg.Socket, os.Getpid())
}

func (rexCfg *RexConfig) bindRexSocket() (listener *net.UnixListener) {
	if _, err := os.Stat(rexCfg.Socket); err == nil {
		log.Fatalf("Socket '%s' already exists", rexCfg.Socket)
	}

	// Listen for REX socket
	addr, err := net.ResolveUnixAddr("unix", rexCfg.Socket)
	if err != nil {
		log.Fatalln(err)
	}

	listener, err = net.ListenUnix("unix", addr)
	if err != nil {
		log.Fatalln(err)
	}

	// Reduce permissions for REX socket
	gid := 0
	grp, err := user.LookupGroup(rexCfg.SocketGroup)
	if err == nil {
		gid, _ = strconv.Atoi(grp.Gid)
	}
	os.Chown(addr.Name, 0, gid)
	os.Chmod(addr.Name, 0660)

	return
}

// Unlinks rex socket from existence
func (rexCfg *RexConfig) unlinkRexSocket() {
	os.Remove(rexCfg.Socket)
}

// Serves as many connections as possible in rex -t mode
func (rexCfg *RexConfig) serve(listener *net.UnixListener) {
	if rexlib.IsMonitorMode() {
		log.Printf("Started daemon in monitoring mode, pid: %d", os.Getpid())
	} else {
		log.Printf("Started daemon in tracing mode, pid: %d", os.Getpid())
	}

	for {
		conn, err := listener.Accept()
		if err == nil {
			go rpc.ServeConn(conn)
		}
	}
}

// Waits for main() goroutine to connect to us and serves it
func (rexCfg *RexConfig) serveOne(listener *net.UnixListener) {
	log.Printf("Started standalone tracer, pid: %d", os.Getpid())

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalln(err)
		}

		rpc.ServeConn(conn)
	}
}

func (rexCfg *RexConfig) connectRexSocket() (conn *net.UnixConn) {
	addr, err := net.ResolveUnixAddr("unix", rexCfg.Socket)
	if err != nil {
		log.Fatalln(err)
	}

	conn, err = net.DialUnix("unix", nil, addr)
	if err != nil {
		log.Fatalln(err)
	}
	return
}

func (rexCfg *RexConfig) setupRPC(isMon bool) {
	srvHI := new(SRVHostInfo)
	srvHI.initialize()
	rpc.Register(srvHI)

	srvRex := new(SRVRex)
	srvRex.initialize(filepath.Join(rexCfg.DataDir, "incidents"), rexCfg.TSLoadPath, isMon)
	rpc.Register(srvRex)

	if isMon {
		srvMon := new(SRVMon)
		srvMon.initialize(rexCfg.monCfg, filepath.Join(rexCfg.DataDir, "sox"))
		rpc.Register(srvMon)
	}
}

func (rexCfg *RexConfig) waitForExitSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)

	s := <-ch
	log.Print("Got ", s)
}
