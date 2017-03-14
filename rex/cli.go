package main

import (
	"fmt"
	
	"net/rpc"
	
	"rexlib"
	"fishly"
)

type RexContext struct {
	cfg *RexConfig
	client *rpc.Client
	
	incident *rexlib.Incident
}

func (ctx *RexContext) Update(cliCtx *fishly.Context) error {
	var prompt string
	path := cliCtx.GetCurrentState().Path
	
	if ctx.incident != nil {
		prompt = ctx.incident.Name
		if len(path) >= 2 {
			prompt = fmt.Sprint("%s %s", prompt, path[1])
		}
	} 
	
	cliCtx.Prompt = prompt
	return nil
}

func (ctx *RexContext) Cancel(rq *fishly.Request)  {
	// Reconnect client when cancelling operation
	ctx.client.Close()
	ctx.client = rpc.NewClient(ctx.cfg.connectRexSocket())
}

func (ctx *RexContext) startCLI(cfg *RexConfig)  {
	var cliCfg fishly.Config
	
	cliCfg.UserConfig = cfg.cliCfg
	
	cliCfg.PromptProgram = "rex"
	cliCfg.PromptSuffix = "> "
	
	cliCfg.Readline = &fishly.CLIReadlineFactory{Config: cfg.cliRLCfg}
	cliCfg.Cancel = &fishly.CLIInterruptHandlerFactory{}
	
	ctx.registerCommands(&cliCfg)
	ctx.cfg = cfg
	
	cliCfg.Run(ctx)
}

func (ctx *RexContext) registerCommands(cliCfg *fishly.Config)  {
	cliCfg.RegisterCommand(new(hostinfoCmd), "hostinfo", "hi")
	
	cliCfg.RegisterCommand(new(incidentListCmd), "incident", "ls")
	cliCfg.RegisterCommand(&incidentCmd{doCreate: true}, "incident", "create")
	cliCfg.RegisterCommand(&incidentCmd{doCreate: false}, "incident", "select")
	cliCfg.RegisterCommand(new(incidentRemoveCmd), "incident", "rm")
	
	cliCfg.RegisterCommand(&incidentSetCmd{nextState: rexlib.IncCreated}, "incident", "set")
	cliCfg.RegisterCommand(&incidentSetCmd{nextState: rexlib.IncRunning}, "incident", "start")
	cliCfg.RegisterCommand(&incidentSetCmd{nextState: rexlib.IncStopped}, "incident", "stop")
}
