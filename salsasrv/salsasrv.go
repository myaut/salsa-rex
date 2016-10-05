package main

import (
	"os"
	"fmt"
	"log"
	"flag"
	"strconv"
	
	"syscall"
	
	"net/http"
	
	"salsarex"
	"salsarex/indexer"
	
	"github.com/go-ini/ini"
	
	_ "net/http/pprof"
)

func main() {
	configPath := flag.String("config", "", "path to config.ini")
	backend := flag.Bool("backend", false, "start as front-end server")
    flag.Parse()
	
	if len(*configPath) == 0 {
		flag.Usage()
		log.Fatalln("Missing -config option")
	}
	
	log.Printf("Loading config from %s", *configPath)
	cfg, err := ini.Load(*configPath)
	if err != nil {
		log.Fatalln(err)
	}
	
	err = initializeDatabase(cfg)
	if err != nil {
		log.Fatalln(err)
	}
	
	err = setupProcess(cfg)
	if err != nil {
		log.Println(err)
	}
	
	registerIndexers()
	
	log.Fatalln(startServer(cfg, *backend))
}
 
func initializeDatabase(cfg *ini.File) (err error) {
	// load database config
	var dbCfg salsarex.DBConfig
	err = cfg.Section("database").MapTo(&dbCfg)
	if err != nil || len(dbCfg.URL) == 0 || len(dbCfg.Username) == 0 {
		return fmt.Errorf("Error in database configuration")
	}
	
	log.Printf("Connecting to arango database at %s...", dbCfg.URL)
	
	return salsarex.InitializeDB(&dbCfg)
}

func startServer(cfg *ini.File, backend bool) (err error) {
	srvCfg := struct {
		Hostname string
		Port int
		APIRoot string
		XREFRoot string
	} {
		"localhost",
		80,
		"/api",
		"/xref",
	}
	
	err = cfg.Section("salsasrv").MapTo(&srvCfg)
	if err != nil {
		return fmt.Errorf("Error in server configuration: %v", err)
	}
	
	addr := fmt.Sprintf("%s:%d", srvCfg.Hostname, srvCfg.Port)
	
	if backend {
		// create special multiplexer for /api on backend
		apiMux := createBackendAPIHandlers()
		http.Handle(srvCfg.APIRoot + "/", http.StripPrefix(srvCfg.APIRoot, apiMux))
		
		log.Printf("Starting backend API server on http://%s%s...", addr, srvCfg.APIRoot)		
	} else {
		// create multiplexers for /api and /xref
		apiMux := createFrontendAPIHandlers()
		http.Handle(srvCfg.APIRoot + "/", http.StripPrefix(srvCfg.APIRoot, apiMux))
		
		log.Printf("Starting frontend API server on http://%s%s...", addr, srvCfg.APIRoot)
	}
	
	return http.ListenAndServe(addr, nil)
}

func setupProcess(cfg *ini.File) (err error) {
	procCfg := struct {
		NoFile uint64
		MallocArenaMax int
		MaxProcessingRoutines int
	}{}
	err = cfg.Section("process").MapTo(&procCfg)
		
	if err == nil && procCfg.NoFile > 0 {
		// increase/decrease file limit according to config
		err = setRLimit(syscall.RLIMIT_NOFILE, procCfg.NoFile)
	}
	
	if err == nil && procCfg.MallocArenaMax > 0 {
		err = os.Setenv("MALLOC_ARENA_MAX", strconv.Itoa(procCfg.MallocArenaMax))
	}
	
	if err == nil && procCfg.MaxProcessingRoutines > 0 {
		salsarex.SetMaxProcessingRoutines(procCfg.MaxProcessingRoutines)
	}
	
	return
}

func setRLimit(resource int, limit uint64) (err error) {
	var rLimit syscall.Rlimit
	
	err = syscall.Getrlimit(resource, &rLimit)
	if err != nil {
		return err
	}
	
	rLimit.Cur = limit
	return syscall.Setrlimit(resource, &rLimit)
}

// indexers global variables
var identifierIndexer = indexer.NewIdentifierIndexer()

func registerIndexers() {
	salsarex.RegisterIndexer(identifierIndexer)
}
