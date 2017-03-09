package main

import (
	"net/rpc"
	
	"fishly"
)

type RexContext struct {
	client *rpc.Client
}

func (ctx *RexContext) Update(cliCtx *fishly.Context) error {
	return nil
}

func (ctx *RexContext) Cancel(rq *fishly.Request)  {
	
}

func (ctx *RexContext) startCLI(cfg *RexConfig)  {
	var cliCfg fishly.Config
	
	cliCfg.UserConfig = cfg.cliCfg
	
	cliCfg.PromptProgram = "rex"
	cliCfg.PromptSuffix = "> "
	
	cliCfg.Readline = &fishly.CLIReadlineFactory{Config: cfg.cliRLCfg}
	cliCfg.Cancel = &fishly.CLIInterruptHandlerFactory{}
	
	ctx.registerCommands(&cliCfg)
	
	cliCfg.Run(ctx)
}

func (ctx *RexContext) registerCommands(cliCfg *fishly.Config)  {
	cliCfg.RegisterCommand(new(hostinfoCmd), "hostinfo", "hi")
}
