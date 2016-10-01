package main

import (
	"fmt"
	"log"
	"flag"
	
	"syscall"
	
	"net/http"
	
	"salsarex"
	
	"github.com/go-ini/ini"
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
	
	err = setFileLimit()
	if err != nil {
		log.Println(err)
	}
	
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

func setFileLimit() error {
	// increase file limit up to maximum allowed
	var rLimit syscall.Rlimit
	
	err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	if err != nil {
		return err
	}
	
	rLimit.Cur = rLimit.Max;
	return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit)
}
