package main

import (
	"os"
	"fmt"
	"log"
	"flag"
	"strconv"
	
	"syscall"
	
	"salsalib"
	"salsalib/indexer"
	
	"github.com/go-ini/ini"
	
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/labstack/echo/engine/standard"
	
	"net/http"
	_ "net/http/pprof"
)

var configPath *string = flag.String("config", "", "path to config.ini")
var backend *bool = flag.Bool("backend", false, "start as front-end server")
var logDb *bool = flag.Bool("log-db", false, "log database operations")

func main() {
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
	
	log.Fatalln(startServer(cfg))
}
 
func initializeDatabase(cfg *ini.File) (err error) {
	// load database config
	var dbCfg salsalib.DBConfig
	err = cfg.Section("database").MapTo(&dbCfg)
	if err != nil || len(dbCfg.URL) == 0 || len(dbCfg.Username) == 0 {
		return fmt.Errorf("Error in database configuration")
	}
	
	log.Printf("Connecting to arango database at %s...", dbCfg.URL)
	
	return salsalib.InitializeDB(&dbCfg, *logDb)
}

func startServer(cfg *ini.File) (err error) {
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
	
	server := echo.New()
	
	// TODO: disable debug:
	
	// setup logging
	middleware.DefaultLoggerConfig.Format = 
		 `${time_rfc3339} ${remote_ip} ${method} ${uri} | ${status} ${latency_human} ${bytes_in}/${bytes_out}` + "\n";
	server.Use(middleware.Logger())
	
	go http.ListenAndServe(":6060", nil)	// for pprof
	
	server.SetHTTPErrorHandler(func(err error, ctx echo.Context) {
		log.Printf("ERROR: %s %s -> %v\n", ctx.Request().Method(), 
						ctx.Request().URL().Path(), err)
		
		server.DefaultHTTPErrorHandler(err, ctx)
	})
	
	// setup server routing & start
	addr := fmt.Sprintf("%s:%d", srvCfg.Hostname, srvCfg.Port)
	
	if *backend {
		// create special multiplexer for /api on backend
		createBackendAPIHandlers(srvCfg.APIRoot, server)
		
		log.Printf("Starting backend API server on http://%s%s...", addr, srvCfg.APIRoot)		
	} else {
		// create multiplexers for /api and /xref
		createFrontendAPIHandlers(srvCfg.APIRoot, server)
		
		log.Printf("Starting frontend API server on http://%s%s...", addr, srvCfg.APIRoot)
	}
	
	return server.Run(standard.New(addr))
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
		salsalib.SetMaxProcessingRoutines(procCfg.MaxProcessingRoutines)
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
var identifierIndexerFactory = new(indexer.IdentifierIndexerFactory)

func registerIndexers() {
	salsalib.RegisterIndexer(identifierIndexerFactory)
}

