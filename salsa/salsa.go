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
	autoExec := flag.String("exec", "", "command to be automatically executed")
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
	
	cliCfg.UserConfig.AutoExec = *autoExec
	
	// Fill in commands list and run
	registerCLICommands(&cliCfg)
	cliCfg.Run()
}

func setupCLI(cfg *ini.File, cliCfg *fishly.Config) error {
	// Setup cli environment
	err := cfg.Section("cli").MapTo(&cliCfg.UserConfig)
	if err == nil {
		// Slices should be merged properly
		baseHelp := cliCfg.UserConfig.Help
		baseStyle := cliCfg.UserConfig.StyleSheet
		
		err = cfg.Section("salsa-cli").MapTo(&cliCfg.UserConfig)
		
		cliCfg.UserConfig.Help = append(cliCfg.UserConfig.Help, baseHelp...)
		cliCfg.UserConfig.StyleSheet = append(cliCfg.UserConfig.StyleSheet, baseStyle...)
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

func registerCLICommands(cliCfg *fishly.Config) {
	cliCfg.RegisterCommand(new(listReposCmd), "repository", "ls")
	cliCfg.RegisterCommand(new(selectRepoCmd), "repository", "select")
}

func promptFormatter(ctx *fishly.Context) string {
	// TODO
	return ""
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
