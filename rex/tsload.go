package main

import (
	"fmt"

	"fishly"

	"rexlib/tsload"
)

// Helpers

// Returns pointer to actual expirement, or if not created, to new one (useful
// for options creation during help generation)
func (ctx *RexContext) getExperiment(makeNew bool) (exp *tsload.Experiment) {
	if ctx.incident != nil {
		exp = ctx.incident.Experiment
		if exp != nil {
			return
		}
	}

	if makeNew {
		exp = tsload.NewExperiment("", 0)
	}
	return
}

func (ctx *RexContext) getWorkload() (exp *tsload.Experiment,
	wl *tsload.Workload, err error) {

	exp = ctx.getExperiment(false)
	if exp == nil {
		return nil, nil, fmt.Errorf("Invalid incident or experiment")
	}

	wl, ok := exp.Workloads[ctx.TSLoadWorkload]
	if !ok || wl == nil {
		return nil, nil, fmt.Errorf("Invalid workload name '%s'", ctx.TSLoadWorkload)
	}

	return exp, wl, nil
}

//
// 'tsload' command starts defining tsexperiment and changes context/executes
// subcommmands in them
//

type tsloadCmd struct {
	fishly.HandlerWithoutCompletion
	fishly.HandlerWithoutOptions
}

func (cmd *tsloadCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	if ctx.isMonitor {
		return false
	}
	return ctx.incident != nil
}

func (*tsloadCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)

	err = ctx.refreshIncident()
	if err != nil {
		return
	}

	if ctx.incident.Experiment == nil {
		ctx.incident.Experiment = tsload.NewExperiment(ctx.incident.Name,
			int64(ctx.incident.TickInterval)*1000000)
	}

	// If there is subblock in here, try to execute it
	state := cliCtx.GetCurrentState()
	cliCtx.PushState(false)
	ctx.TSLoadExperimentMode = true
	if cliCtx.ProcessBlock(rq) == nil {
		cliCtx.RestoreState(state)
	}

	return ctx.saveIncident()
}

//
// Helper IsApplicable() mixins for workload and experiment commands
//

type tsloadExperimentCmd struct {
}

func (cmd *tsloadExperimentCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.TSLoadExperimentMode
}

type tsloadExperimentWorkloadCmd struct {
}

func (cmd *tsloadExperimentWorkloadCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.TSLoadExperimentMode && len(ctx.TSLoadWorkload) > 0
}

//
// 'threadpool' command defines new threadpool inside TSLoad experiment.
// It doesn't change context: all parameters can be set in a single command
//

type tsloadThreadPoolCmd struct {
	tsloadExperimentCmd
	fishly.HandlerWithoutCompletion
}

func (cmd *tsloadThreadPoolCmd) NewOptions(cliCtx *fishly.Context) interface{} {
	ctx := cliCtx.External.(*RexContext)
	return ctx.getExperiment(true).NewThreadPool()
}

func (*tsloadThreadPoolCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)

	exp := ctx.getExperiment(false)
	if exp == nil {
		return fmt.Errorf("Invalid incident or experiment")
	}

	tp := rq.Options.(*tsload.ThreadPool)
	exp.ThreadPools[tp.Name] = tp

	return ctx.saveIncident()
}

//
// 'workload' command adds new workload. Changes context to that workload
// and allows to define its steps and parameter values.
//

type tsloadWorkloadCmd struct {
	tsloadExperimentCmd
	fishly.HandlerWithoutCompletion
}

func (cmd *tsloadWorkloadCmd) NewOptions(cliCtx *fishly.Context) interface{} {
	ctx := cliCtx.External.(*RexContext)
	return ctx.getExperiment(true).NewWorkload()
}

func (*tsloadWorkloadCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)

	exp := ctx.getExperiment(false)
	if exp == nil {
		return fmt.Errorf("Invalid incident or experiment")
	}

	wl := rq.Options.(*tsload.Workload)
	exp.Workloads[wl.Name] = wl

	// Run workload subcommands: param and steps
	state := cliCtx.GetCurrentState()
	cliCtx.PushState(false)
	ctx.TSLoadWorkload = wl.Name
	if cliCtx.ProcessBlock(rq) == nil {
		cliCtx.RestoreState(state)
	}

	return ctx.saveIncident()
}

//
// 'param' command sets workload parameter
//

type tsloadWLParamCmd struct {
	tsloadExperimentWorkloadCmd
	fishly.HandlerWithoutCompletion
}

func (cmd *tsloadWLParamCmd) NewOptions(cliCtx *fishly.Context) interface{} {
	return &tsload.WLParam{}
}

func (*tsloadWLParamCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)
	_, wl, err := ctx.getWorkload()
	if err != nil {
		return
	}

	param := rq.Options.(*tsload.WLParam)
	wl.Parameters.Params = append(wl.Parameters.Params, param)

	return ctx.saveIncident()
}

//
// 'steps' command defines workload steps
//

type tsloadWLStepsCmd struct {
	tsloadExperimentWorkloadCmd
	fishly.HandlerWithoutCompletion
}

func (cmd *tsloadWLStepsCmd) NewOptions(cliCtx *fishly.Context) interface{} {
	return &tsload.WLSteps{}
}

func (*tsloadWLStepsCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)
	exp, _, err := ctx.getWorkload()
	if err != nil {
		return
	}

	exp.Steps[ctx.TSLoadWorkload] = rq.Options.(*tsload.WLSteps)
	return ctx.saveIncident()
}
