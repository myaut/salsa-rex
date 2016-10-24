package main

import (
	"os"
	"os/user"
	"log"
	
	"fmt"
	"strings"
	
	"flag"
	
	"fishly"
	
	"github.com/go-ini/ini"
)

func main() {
	// Find default config location -- either it provided with -config
	// or located in ~/.salsarc
	configPath := flag.String("config", handleHome("~/.salsarc"), "path to client.ini")
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
	
	err = setupCLI(cfg, &cliCfg)
	if err != nil {
		log.Fatalln(err)
	}
	
	// Fill in commands list and run
	cliCfg.Commands = []fishly.Command {
		new(listReposCmd),
		new(selectReposCmd),
	}
	
	fishly.Run(&cliCfg)
}

func setupCLI(cfg *ini.File, cliCfg *fishly.Config) error {
	// Setup cli environment
	err := cfg.Section("cli").MapTo(&cliCfg.UserConfig)
	if err != nil {
		cfg.Section("salsa-cli").MapTo(&cliCfg.UserConfig)
	}
	if err != nil {
		return fmt.Errorf("Error in CLI configuration: %s", err)
	}
	
	// Handle history file properly
	cliCfg.UserConfig.HistoryFile = handleHome(cliCfg.UserConfig.HistoryFile)
	
	// Setup prompt formatter
	cliCfg.PromptProgram = "salsa"
	cliCfg.PromptSuffix = "> "
	cliCfg.PromptFormatter = promptFormatter
	return nil
}

func promptFormatter(ctx *fishly.Context) string {
	// TODO
	return ""
}

func handleHome(path string) string {
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
