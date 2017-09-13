package main

import (
	"fmt"

	"net/rpc"
	"strconv"

	"fishly"
	"rexlib"
)

type RexContext struct {
	cfg    *RexConfig
	client *rpc.Client

	incident      *rexlib.Incident
	providerIndex int

	tsloadExperimentMode bool
	tsloadWorkload       string
}

func (ctx *RexContext) Update(cliCtx *fishly.Context) (err error) {
	var prompt string
	path := cliCtx.GetCurrentState().Path

	if len(path) > 0 && (ctx.incident == nil || ctx.incident.Name != path[0]) {
		ctx.incident = new(rexlib.Incident)
		err = ctx.client.Call("SRVRex.GetIncident", &path[0], ctx.incident)
		if err != nil {
			ctx.incident = nil
			return
		}
	}

	ctx.reset()
	if ctx.incident != nil {
		prompt = ctx.incident.Name

		if len(path) >= 2 && path[1] == "tsload" {
			ctx.tsloadExperimentMode = true
			prompt = fmt.Sprintf("%s [tsload]", prompt)

			if len(path) >= 3 {
				ctx.tsloadWorkload = path[2]
				prompt = fmt.Sprintf("%s/%s", prompt, ctx.tsloadWorkload)
			}
		} else if len(path) >= 3 {
			prompt = fmt.Sprintf("%s %s#%s", prompt, path[1], path[2])

			switch path[1] {
			case "prov":
				pi, _ := strconv.ParseInt(path[2], 10, 32)
				ctx.providerIndex = int(pi)
			}
		}
	}

	cliCtx.Prompt = prompt
	return
}

// reset context fields (except incident itself) before updating according
// to a context
func (ctx *RexContext) reset() {
	ctx.providerIndex = -1
	ctx.tsloadExperimentMode = false
	ctx.tsloadWorkload = ""
}

func (ctx *RexContext) Cancel(rq *fishly.Request) {
	// Reconnect client when cancelling operation
	ctx.client.Close()
	ctx.client = rpc.NewClient(ctx.cfg.connectRexSocket())
}

func (ctx *RexContext) startCLI(cfg *RexConfig, autoExec, initContext string) {
	var cliCfg fishly.Config

	cliCfg.UserConfig = cfg.cliCfg

	cliCfg.PromptProgram = "rex"
	cliCfg.PromptSuffix = "> "

	cliCfg.Readline = &fishly.CLIReadlineFactory{Config: cfg.cliRLCfg}
	cliCfg.Cancel = &fishly.CLIInterruptHandlerFactory{}

	cliCfg.AutoExec = autoExec
	cliCfg.InitContextURL = initContext

	ctx.registerCommands(&cliCfg)
	ctx.cfg = cfg

	cliCfg.Run(ctx)
}

func (ctx *RexContext) registerCommands(cliCfg *fishly.Config) {
	cliCfg.RegisterCommand(new(hostinfoCmd), "hostinfo", "hi")

	cliCfg.RegisterCommand(new(incidentListCmd), "incident", "ls")
	cliCfg.RegisterCommand(&incidentCmd{doCreate: true}, "incident", "create")
	cliCfg.RegisterCommand(&incidentCmd{doCreate: false}, "incident", "select")
	cliCfg.RegisterCommand(new(incidentRemoveCmd), "incident", "rm")

	cliCfg.RegisterCommand(&incidentSetCmd{nextState: rexlib.IncCreated}, "incident", "update")
	cliCfg.RegisterCommand(&incidentSetCmd{nextState: rexlib.IncRunning}, "incident", "start")
	cliCfg.RegisterCommand(&incidentSetCmd{nextState: rexlib.IncStopped}, "incident", "stop")

	cliCfg.RegisterCommand(&incidentProviderCmd{isSet: false}, "incident", "add")
	cliCfg.RegisterCommand(&incidentProviderCmd{isSet: true}, "incident", "set")

	cliCfg.RegisterCommand(&tsloadCmd{}, "tsload", "tsload")
	cliCfg.RegisterCommand(&tsloadThreadPoolCmd{}, "tsload", "threadpool")
	cliCfg.RegisterCommand(&tsloadWorkloadCmd{}, "tsload", "workload")
	cliCfg.RegisterCommand(&tsloadWLParamCmd{}, "tsload", "param")
	cliCfg.RegisterCommand(&tsloadWLStepsCmd{}, "tsload", "steps")
}
