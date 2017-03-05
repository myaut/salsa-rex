package main

import (
	"os"
	"os/user"
	"log"
	
	"fmt"
	"strings"
	
	"flag"
	
	"fishly"
	"salsacore/client"
	
	"github.com/go-ini/ini"
)

const (
	defaultServerName = "main"
)

func main() {
	// Find default config location -- either it provided with -config
	// or located in ~/.salsarc
	configPath := flag.String("config", handleHome("~/.salsarc"), "path to client.ini")
	autoExec := flag.String("exec", "", "command to be automatically executed")
	initContext := flag.String("ctx", "", "initial context state")
	flag.Parse()
	
	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		log.Fatalf("Config '%s' doesn't exist", *configPath)
	}
	
	// Now load the config
	cfg, err := ini.Load(*configPath)
	if err != nil {
		log.Fatalln(err)
	}
	
	var cliCfg fishly.Config
	salsaCtx := NewSalsaContext()
	
	err = setupCLI(cfg, &cliCfg)
	if err != nil {
		log.Fatalln(err)
	}
	
	err = loadServers(cfg, salsaCtx)
	if err != nil {
		log.Fatalln(err)
	}
	
	cliCfg.UserConfig.AutoExec = *autoExec
	cliCfg.UserConfig.InitContextURL = *initContext
	
	// Fill in commands list and run
	registerCLICommands(&cliCfg)
	cliCfg.Run(salsaCtx)
}

func setupCLI(cfg *ini.File, cliCfg *fishly.Config) error {
	// Setup cli environment 
	var rlCfg fishly.ReadlineConfig
	err := cfg.Section("cli").MapTo(&cliCfg.UserConfig)
	if err == nil {
		err = cfg.Section("cli").MapTo(&rlCfg)
	}
	if err != nil {
		return fmt.Errorf("Error in CLI configuration: %s", err)
	}
	
	// Load schema paths and redefinitions and merge slices
	baseSchema := cliCfg.UserConfig.Schema
	cfg.Section("salsa-cli").MapTo(&cliCfg.UserConfig)
	cfg.Section("salsa-cli").MapTo(&rlCfg)	
	cliCfg.UserConfig.Schema = append(cliCfg.UserConfig.Schema, baseSchema...)
	
	// Handle history file properly
	// cliCfg.UserConfig.HistoryFile = handleHome(cliCfg.UserConfig.HistoryFile)
	
	// Setup prompt formatter
	cliCfg.PromptProgram = "salsa"
	cliCfg.PromptSuffix = "> "
	
	// Setup term driver
	cliCfg.Readline = &fishly.CLIReadlineFactory{Config: rlCfg}
	cliCfg.Cancel = &fishly.CLIInterruptHandlerFactory{}
	
	return nil
}

func loadServers(cfg *ini.File, ctx *SalsaContext) error {
	for _, section := range cfg.Sections() {
		if !strings.HasPrefix(section.Name(), "salsasrv") {
			continue
		}
		
		// If no suffix is provided use name "main"
		var srv client.ServerConnection
		srv.Name = defaultServerName
		index := strings.Index(section.Name(), "-")
		if index > 0 {
			srv.Name = section.Name()[index+1:]
		} 
		
		err := section.MapTo(&srv)
		if err != nil {
			return err
		}
		
		ctx.handle.Servers = append(ctx.handle.Servers, srv) 
	} 
	
	return nil
}

func registerCLICommands(cliCfg *fishly.Config) {
	cliCfg.RegisterCommand(new(listReposCmd), "repository", "ls")
	cliCfg.RegisterCommand(new(selectRepoCmd), "repository", "select")
	cliCfg.RegisterCommand(new(listFilesCmd), "repofs", "ls")
	cliCfg.RegisterCommand(new(changePathCmd), "repofs", "cd")
	cliCfg.RegisterCommand(new(printFileCmd), "repofs", "cat")
}

func handleHome(path string) string {
	// TODO: remove this debugging hack
	if strings.HasSuffix(path, ".salsarc") {
		return "client.ini"
	}
	
	usr, _ := user.Current()
	homeDir := "/tmp"
	if usr != nil {
		homeDir = usr.HomeDir
	}
	
	if strings.HasPrefix(path, "~/") {
		return strings.Replace(path, "~", homeDir, 1)
	}
	
	return path
}
