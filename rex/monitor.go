package main

import (
	"os/user"

	"strings"

	"net/url"

	"fishly"
	"rexlib"
)

type SRVMon struct{}

// userName and keyPath are for unix socket ssh redirection, if omitted,
// current user is used, supports tilde expansion in key path
// unixSockPath is used as default socket path if wasn't provided
func (srv *SRVMon) initialize(monCfg RexMonConfig, sockDir string) (err error) {

	// Resolve user (if not provided)
	var usr *user.User
	if len(monCfg.User) == 0 {
		usr, err = user.Current()
	} else {
		usr, err = user.Lookup(monCfg.User)
	}
	if err != nil {
		return err
	}
	monCfg.User = usr.Name

	// Expand tilde as specified user home directory
	keyPath := monCfg.Key
	if strings.HasPrefix(keyPath, "~/") {
		keyPath = strings.Replace(keyPath, "~", usr.HomeDir, 1)
	}

	// Expand URLs for provided hosts
	urls := make([]*url.URL, 0, len(monCfg.Hosts))
	for _, host := range monCfg.Hosts {
		sockUrl, err := url.Parse(host)
		if err != nil {
			return err
		}

		if len(sockUrl.Path) == 0 && sockUrl.Scheme == "unix" {
			sockUrl.Path = monCfg.Socket
		}

		urls = append(urls, sockUrl)
	}

	return rexlib.InitializeMonitor(usr.Name, keyPath, sockDir, urls)
}

func (srv *SRVMon) GetMonitoredHosts(args *struct{}, reply *[]string) (err error) {
	*reply = rexlib.GetMonitoredHosts()
	return nil
}

func (srv *SRVMon) GetIncidentList(host *string, reply *[]rexlib.IncidentDescriptor) (err error) {
	clnt, err := rexlib.Connect(*host)
	if err != nil {
		return
	}

	return clnt.Call("SRVRex.GetIncidentList", &struct{}{}, reply)
}

type IncidentImportArgs struct {
	Host     string
	Incident string
}

func (srv *SRVMon) ImportIncident(args *IncidentImportArgs, reply *rexlib.Incident) (err error) {
	incident, err := rexlib.ImportIncident(args.Host, args.Incident)
	if err != nil {
		return
	}

	*reply = *incident
	return
}

// --------------
// CLI

type incidentImportCmd struct{}
type incidentImportOpt struct {
	Host     string `arg:"1"`
	Incident string `arg:"2"`
}

func (cmd *incidentImportCmd) NewOptions(ctx *fishly.Context) interface{} {
	return new(incidentImportOpt)
}

func (cmd *incidentImportCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.isMonitor
}

func (cmd *incidentImportCmd) Complete(cliCtx *fishly.Context, rq *fishly.CompleterRequest) {
	ctx := cliCtx.External.(*RexContext)
	switch rq.ArgIndex {
	case 1:
		hosts := ctx.getMonitoredHosts()
		rq.AddOptions(hosts...)
	case 2:
		ctx := cliCtx.External.(*RexContext)
		opt := rq.GetExistingOptions().(*incidentImportOpt)

		rq.AddOptions(ctx.getIncidentNames(opt.Host)...)
	}
}

func (cmd *incidentImportCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)
	opts := rq.Options.(*incidentImportOpt)

	incident := new(rexlib.Incident)

	err = ctx.client.Call("SRVMon.ImportIncident", &IncidentImportArgs{
		Incident: opts.Incident, Host: opts.Host}, incident)
	if err != nil {
		return
	}

	cliCtx.PushState(true).Reset(incident.Name)
	ctx.incident = incident
	return ctx.refreshIncident()
}
