package main

import (
	"fmt"
	"log"

	"fishly"
	"rexlib"
)

type SRVYa struct{}

func (ya *SRVYa) initialize(templatePath, learningPath string) error {
	return rexlib.InitializeYatima(templatePath, learningPath)
}

func (srv *SRVYa) GetTrainingSession(name *string, reply *rexlib.TrainingSession) error {
	session, ok := rexlib.Training.Get(*name)
	if !ok {
		return fmt.Errorf("Session '%s' was not found", *name)
	}

	*reply = *session
	return nil
}

func (srv *SRVYa) GetTrainingSessionsList(args *struct{}, reply *[]string) error {
	sessions := rexlib.Training.List()
	*reply = sessions

	return nil
}

func (srv *SRVYa) RunTraining(session *rexlib.TrainingSession, reply *struct{}) (err error) {
	return rexlib.Training.Run(session)
}

func (srv *SRVYa) RemoveTrainingSessions(names []string, reply *struct{}) error {
	return rexlib.Training.Remove(names...)
}

// --------------
// CLI

func (ctx *RexContext) getTrainingSessionNames() (names []string, err error) {
	err = ctx.client.Call("SRVYa.GetTrainingSessionsList", &struct{}{}, &names)
	return
}

//
// 'training' command in any context toggles training mode, thus you can't
// work with anything until you rerun it
//

type toggleTrainingCmd struct {
	fishly.HandlerWithoutCompletion
	fishly.HandlerWithoutOptions
}

func (cmd *toggleTrainingCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.isMonitor && ctx.incident == nil
}

func (cmd *toggleTrainingCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) error {
	ctx := cliCtx.External.(*RexContext)

	ctx.TrainingMode = !ctx.TrainingMode
	if ctx.TrainingMode {
		state := cliCtx.GetCurrentState()
		cliCtx.PushState(false)
		log.Print("Enabled training mode")
		if cliCtx.ProcessBlock(rq) == nil {
			cliCtx.RestoreState(state)
		}
	} else {
		cliCtx.PushState(true).Reset()
	}

	return nil
}

//
// 'train' cmd in the incident context -- a simplest case which trains
// using data of a single incident
//

type incidentTrainingCmd struct {
	fishly.HandlerWithoutCompletion
	fishly.HandlerWithoutOptions
}

func (cmd *incidentTrainingCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.isMonitor && ctx.incident != nil
}

func (cmd *incidentTrainingCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)
	session := &rexlib.TrainingSession{
		Name:      ctx.incident.Name,
		Incidents: []string{ctx.incident.Name},
	}

	return ctx.client.Call("SRVYa.RunTraining", session, &struct{}{})
}

// base command for commands that are available only in training mode
type trainingBaseCmd struct {
}

func (cmd *trainingBaseCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.TrainingMode
}

//
// 'ls' command in the context of training mode, enables
//

type trainingListCmd struct {
	fishly.HandlerWithoutCompletion
	fishly.HandlerWithoutOptions

	trainingBaseCmd
}

func (cmd *trainingListCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)

	sessions, err := ctx.getTrainingSessionNames()
	if err != nil {
		return err
	}

	ioh, err := rq.StartOutput(cliCtx, false)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()

	ioh.StartObject("sessions")
	for _, session := range sessions {
		ioh.StartObject("session")
		ioh.WriteString("name", session)
		ioh.EndObject()
	}
	ioh.EndObject()

	return
}

//
// 'select' command selects training session (or deselects it when called without
// argument)
//

type trainingSelectCmd struct {
	trainingBaseCmd
}

type trainingSelectOpt struct {
	Name string `arg:"1,opt"`
}

func (cmd *trainingSelectOpt) NewOptions(ctx *fishly.Context) interface{} {
	return new(trainingSelectOpt)
}

func (cmd *trainingSelectOpt) Complete(cliCtx *fishly.Context, rq *fishly.CompleterRequest) {
	switch rq.ArgIndex {
	case 1:
		ctx := cliCtx.External.(*RexContext)
		names, _ := ctx.getTrainingSessionNames()
		rq.AddOptions(names...)
	}
}

func (cmd *trainingSelectCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)
	opts := rq.Options.(*trainingSelectOpt)
	if len(opts.Name) == 0 {
		cliCtx.PushState(false).Reset("training")
		return nil
	}

	session := new(rexlib.TrainingSession)
	err = ctx.client.Call("SRVYa.GetTrainingSession", opts.Name, session)
	if err == nil {
		ctx.trainingSession = session
		cliCtx.PushState(true).Reset("training", session.Name)
	}

	return
}

//
// 'rm' command removes training sessions
//

type trainingRemoveCmd struct {
	trainingBaseCmd
}
type trainingRemoveOpt struct {
	Names []string `arg:"1"`
}

func (cmd *trainingRemoveCmd) NewOptions(cliCtx *fishly.Context) interface{} {
	return new(trainingRemoveOpt)
}

func (cmd *trainingRemoveCmd) Complete(cliCtx *fishly.Context, rq *fishly.CompleterRequest) {
	if rq.ArgIndex >= 1 {
		ctx := cliCtx.External.(*RexContext)
		names, _ := ctx.getTrainingSessionNames()
		rq.AddOptions(names...)
	}
}

func (cmd *trainingRemoveCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	opts := rq.Options.(*trainingRemoveOpt)
	ctx := cliCtx.External.(*RexContext)
	return ctx.client.Call("SRVYa.RemoveTrainingSessions", opts.Names, &struct{}{})
}
