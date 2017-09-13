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

	wl, ok := exp.Workloads[ctx.tsloadWorkload]
	if !ok || wl == nil {
		return nil, nil, fmt.Errorf("Invalid workload name '%s'", ctx.tsloadWorkload)
	}

	return exp, wl, nil
}

//
// 'tsload' command starts defining tsexperiment
//

type tsloadCmd struct {
	fishly.HandlerWithoutCompletion
	fishly.HandlerWithoutOptions
}

func (cmd *tsloadCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
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

	ctx.tsloadExperimentMode = true

	// If there is subblock in here, try to execute it
	cliCtx.PushState(false).Reset(ctx.incident.Name, "tsload")
	if cliCtx.ProcessBlock(rq) != nil {
		cliCtx.RewindState(-1)
	}

	return ctx.saveIncident()
}

//
// Helper IsApplicable mixins
//

type tsloadExperimentCmd struct {
}

func (cmd *tsloadExperimentCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.tsloadExperimentMode
}

type tsloadExperimentWorkloadCmd struct {
}

func (cmd *tsloadExperimentWorkloadCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.tsloadExperimentMode && len(ctx.tsloadWorkload) > 0
}

//
// 'threadpool' command
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
// 'workload' command
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
	cliCtx.PushState(false).Reset(ctx.incident.Name, "tsload", wl.Name)
	ctx.tsloadWorkload = wl.Name
	if cliCtx.ProcessBlock(rq) != nil {
		cliCtx.RewindState(-1)
	}

	return ctx.saveIncident()
}

//
// 'param' command
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
// 'steps' command
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

	exp.Steps[ctx.tsloadWorkload] = rq.Options.(*tsload.WLSteps)
	return ctx.saveIncident()
}
