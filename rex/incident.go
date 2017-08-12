package main

import (
	"fmt"

	"strings"

	"time"

	"encoding/gob"

	"rexlib"
	"rexlib/provider"

	"fishly"
)

// --------------
// SRV

type SRVRex struct{}

func (srv *SRVRex) initialize(path string) {
	rexlib.Initialize(path)

	gob.Register(&IncidentProviderArgs{})
}

func (srv *SRVRex) CreateIncident(other *rexlib.Incident, reply *rexlib.Incident) (err error) {
	incident, err := rexlib.Incidents.New(other)
	if err != nil {
		return
	}

	*reply = *incident
	return
}

func (srv *SRVRex) GetIncidentList(args *struct{}, reply *[]rexlib.IncidentDescriptor) (err error) {
	incidents, err := rexlib.Incidents.GetList()
	if err != nil {
		return
	}

	*reply = incidents
	return
}

func (srv *SRVRex) GetIncident(name *string, reply *rexlib.Incident) (err error) {
	incident, err := rexlib.Incidents.Get(*name)
	if err != nil {
		return
	}

	*reply = *incident
	return
}

func (srv *SRVRex) SetIncident(local *rexlib.Incident, reply *struct{}) (err error) {
	// Get current state of incident in server (remote) and update
	// it according to changes in in-client state of incident (local)
	remote, err := rexlib.Incidents.Get(local.Name)
	if err != nil {
		return
	}

	switch remote.GetState() {
	case rexlib.IncCreated:
		// Update incident state and if transition is requested, apply it
		err = remote.Merge(local)
		if err != nil {
			return
		}
		if local.GetState() == rexlib.IncCreated {
			return nil
		}

		return remote.Start()
	case rexlib.IncRunning:
		// TODO: add new providers
		if local.GetState() == rexlib.IncStopped {
			return remote.Stop()
		}
	}

	return fmt.Errorf("Unexpected transition %d -> %d", remote.GetState(), local.GetState())
}

func (srv *SRVRex) RemoveIncidents(names []string, reply *struct{}) (err error) {
	return rexlib.Incidents.Remove(names...)
}

type IncidentProviderArgs struct {
	Incident string
	State    provider.ConfigurationState
	Action   provider.ConfigurationAction
}

func (srv *SRVRex) ConfigureIncidentProvider(args *IncidentProviderArgs,
	reply *provider.ConfigurationState) (err error) {
	incident, err := rexlib.Incidents.Get(args.Incident)
	if err != nil {
		return
	}
	if incident.GetState() == rexlib.IncStopped {
		return fmt.Errorf("Cannot configure stopped incidents")
	}

	reply = new(provider.ConfigurationState)
	*reply = args.State

	err = incident.ConfigureProvider(args.Action, reply)
	return
}

// --------------
// CLI

// For auto-complete thingy
func (ctx *RexContext) getIncidentNames() (names []string) {
	var incidents []rexlib.IncidentDescriptor
	err := ctx.client.Call("SRVRex.GetIncidentList", &struct{}{}, &incidents)

	if err == nil {
		for _, incident := range incidents {
			names = append(names, incident.Name)
		}
	}
	return
}

// refreshes context's state of incident by re-fetching it from rex-t
func (ctx *RexContext) refreshIncident() (err error) {
	if ctx.incident == nil {
		return fmt.Errorf("Unexpected refresh in non-incident context")
	}
	return ctx.client.Call("SRVRex.GetIncident", &ctx.incident.Name, ctx.incident)
}

//
// 'create'/'select' command -- creates incident (new or copies) or
// selects old incident
//

type incidentCmd struct {
	fishly.GlobalCommand

	doCreate bool
}

type incidentCmdOpt struct {
	Name string `opt:"create=n|name,opt"`

	Incident string `arg:"1,opt"`
}

func (cmd *incidentCmd) NewOptions() interface{} {
	return new(incidentCmdOpt)
}

func (cmd *incidentCmd) Complete(cliCtx *fishly.Context, rq *fishly.CompleterRequest) {
	switch rq.ArgIndex {
	case 1:
		ctx := cliCtx.External.(*RexContext)
		rq.AddOptions(ctx.getIncidentNames()...)
	}
}

func (cmd *incidentCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	var incident, other rexlib.Incident

	// load other incident
	ctx := cliCtx.External.(*RexContext)
	opts := rq.Options.(*incidentCmdOpt)
	if len(opts.Incident) > 0 {
		err = ctx.client.Call("SRVRex.GetIncident", &opts.Incident, &other)
		if err != nil {
			return
		}
	}

	if cmd.doCreate {
		// 'create [-n NAME] [INCIDENT]'
		if len(opts.Name) > 0 {
			other.Name = opts.Name
		}

		err = ctx.client.Call("SRVRex.CreateIncident", &other, &incident)
	} else {
		if len(other.Name) == 0 {
			// special case for resetting state
			cliCtx.PushState(true).Reset()
			ctx.incident = nil
			return
		}

		// 'select INCIDENT'
		incident = other
	}
	if err != nil {
		return
	}

	// Update state, default path is /incidentName/<prov>
	cliCtx.PushState(true).Reset(incident.Name)
	ctx.incident = &incident

	return
}

//
// 'ls' command lists incidents when in super-root context
//

type incidentListCmd struct {
	fishly.HandlerWithoutOptions
	fishly.HandlerWithoutCompletion
}

func (cmd *incidentListCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.incident == nil && len(cliCtx.GetCurrentState().Variables) == 0
}

func (cmd *incidentListCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	var incidents []rexlib.IncidentDescriptor

	ctx := cliCtx.External.(*RexContext)
	err = ctx.client.Call("SRVRex.GetIncidentList", &struct{}{}, &incidents)
	if err != nil {
		return err
	}

	ioh, err := rq.StartOutput(cliCtx, false)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()

	ioh.StartObject("incidents")
	for _, incident := range incidents {
		ioh.StartObject("incident")

		ioh.WriteString("name", incident.Name)
		ioh.WriteFormattedValue("state", cmd.formatIncidentState(incident.State), incident.State)
		if len(incident.Description) > 0 {
			ioh.WriteString("description", incident.Description)
		}

		ioh.EndObject()
	}
	ioh.EndObject()
	return
}

func (cmd *incidentListCmd) formatIncidentState(state rexlib.IncState) string {
	switch state {
	case rexlib.IncCreated:
		return "CREATED"
	case rexlib.IncRunning:
		return "RUNNING"
	case rexlib.IncStopped:
		return "STOPPED"
	}
	return "UNKNOWN"
}

//
// 'start'/'stop'/'set' incident commands
//

type incidentSetCmd struct {
	fishly.HandlerWithoutCompletion

	// IncCreated for set, IncRunning for start, IncStopped for stop
	nextState rexlib.IncState
}

type incidentSetOpt struct {
	TickInterval int    `opt:"t|tick,opt"`
	Description  string `opt:"d|description,opt"`
}

func (cmd *incidentSetCmd) NewOptions() interface{} {
	if cmd.nextState != rexlib.IncCreated {
		return &struct{}{}
	}

	return new(incidentSetOpt)
}

func (cmd *incidentSetCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	if ctx.refreshIncident() != nil {
		return false
	}

	if cmd.nextState == rexlib.IncCreated {
		if len(cliCtx.GetCurrentState().Path) != 1 {
			return false // 'set' is not applicable outside incident root
		}

		return ctx.incident.GetState() == rexlib.IncCreated
	}
	return ctx.incident.GetState() < cmd.nextState
}

func (cmd *incidentSetCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)
	err = ctx.refreshIncident()
	if err != nil {
		return
	}

	switch cmd.nextState {
	case rexlib.IncCreated:
		// 'update'
		opt := rq.Options.(*incidentSetOpt)
		if opt.TickInterval > 0 {
			ctx.incident.TickInterval = opt.TickInterval
		}
		if len(opt.Description) > 0 {
			ctx.incident.Description = opt.Description
		}
	case rexlib.IncRunning:
		// 'start'
		ctx.incident.StartedAt = time.Now()
	case rexlib.IncStopped:
		// 'stop'
		ctx.incident.StoppedAt = time.Now()
	}

	return ctx.client.Call("SRVRex.SetIncident", ctx.incident, &struct{}{})
}

//
// 'rm' command removes incidents
//

type incidentRemoveCmd struct {
}
type incidentRemoveOpt struct {
	Names []string `arg:"1"`
}

func (cmd *incidentRemoveCmd) NewOptions() interface{} {
	return new(incidentRemoveOpt)
}

func (cmd *incidentRemoveCmd) Complete(cliCtx *fishly.Context, rq *fishly.CompleterRequest) {
	if rq.ArgIndex >= 1 {
		ctx := cliCtx.External.(*RexContext)
		rq.AddOptions(ctx.getIncidentNames()...)
	}
}

func (cmd *incidentRemoveCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.incident == nil && len(cliCtx.GetCurrentState().Variables) == 0
}

func (cmd *incidentRemoveCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	opts := rq.Options.(*incidentRemoveOpt)
	ctx := cliCtx.External.(*RexContext)
	return ctx.client.Call("SRVRex.RemoveIncidents", opts.Names, &struct{}{})
}

//
// 'set'/'add' commands for configuring providers
//

type incidentProviderCmd struct {
	isSet bool
}

type incidentProviderOpt struct {
	Committed    bool     `opt:"commit,opt"`
	ProviderName string   `arg:"add=1"`
	Arguments    []string `arg:"set=1,opt;add=2,opt"`
}

func (cmd *incidentProviderCmd) NewOptions() interface{} {
	return new(incidentProviderOpt)
}

func (cmd *incidentProviderCmd) Complete(cliCtx *fishly.Context, rq *fishly.CompleterRequest) {
	// TODO
}

func (cmd *incidentProviderCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	if ctx.refreshIncident() != nil {
		return false
	}

	if ctx.incident.GetState() == rexlib.IncStopped {
		return false
	}
	if cmd.isSet && ctx.providerIndex < 0 {
		return false
	}

	return true
}

func (cmd *incidentProviderCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)

	var args IncidentProviderArgs
	args.Incident = ctx.incident.Name
	args.Action = provider.ConfigureSetValue
	cmd.stateFromOptions(&args.State, cliCtx, rq.Options)

	var nextState provider.ConfigurationState
	err = ctx.client.Call("SRVRex.ConfigureIncidentProvider", &args, &nextState)
	if err != nil {
		return
	}

	if nextState.Committed != 0 {
		cliCtx.PushState(true).Reset(ctx.incident.Name)
		ctx.providerIndex = -1
	} else if !cmd.isSet {
		cliCtx.PushState(false).Reset(ctx.incident.Name, "prov", fmt.Sprint(nextState.ProviderIndex))
		ctx.providerIndex = nextState.ProviderIndex
	}
	return
}

func (cmd *incidentProviderCmd) stateFromOptions(state *provider.ConfigurationState,
	cliCtx *fishly.Context, rqOptions interface{}) {

	opts := rqOptions.(*incidentProviderOpt)

	if !cmd.isSet {
		state.ProviderIndex = -1
		state.Configuration = append(state.Configuration, &provider.ConfigurationStep{
			Values: []string{opts.ProviderName},
		})
	} else {
		ctx := cliCtx.External.(*RexContext)
		state.ProviderIndex = ctx.providerIndex
	}

	if opts.Committed {
		state.Committed = 1
	}

	// Now parse provider configuration options in format [[ns:]name=]val1[,val2]
	for _, arg := range opts.Arguments {
		step := new(provider.ConfigurationStep)

		values := arg
		iEq := strings.IndexRune(arg, '=')
		iNsSep := strings.IndexRune(arg, ':')

		if iEq >= 0 {
			if iNsSep >= iEq {
				iNsSep = -1
			}

			values = arg[iEq+1:]
			step.Name = arg[iNsSep+1 : iEq]
			if iNsSep >= 0 {
				step.NameSpace = arg[:iNsSep]
			}
		}

		step.Values = strings.Split(values, ",")
		state.Configuration = append(state.Configuration, step)
	}
}
