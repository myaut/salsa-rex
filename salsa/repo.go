package main 

import (
	"fmt"
	
	"fishly"
)

// 
// ls command for repositories -- shows list of repositories
// 

type listReposCmd struct {
}

func (*listReposCmd) GetDescriptor() fishly.CommandDescriptor {
	return fishly.CommandDescriptor{
		Name: "ls",
		Help: "Shows list of salsa repositories",
		Group: "Repository",
	}
}
func (*listReposCmd) IsApplicable(ctx *fishly.Context) bool {
	return len(ctx.GetCurrentState().Path) == 0	// only applicable when no repository is picked
}
func (*listReposCmd) NewOptions() interface{} {
	return nil		// doesn't support options or arguments
}
func (*listReposCmd) Complete(ctx *fishly.Context, option string) []string {
	return []string{}
}

func (*listReposCmd) Execute(ctx *fishly.Context, rq *fishly.Request) error {
	// TODO
	return fmt.Errorf("Not implemented")
}

// 
// Select an active repository using select command
// 

type selectReposCmd struct {
	
}
type selectReposOpt struct {
	Server string `opt:"server|s,opt" 
				   help:"If multiple salsasrv servers provide same repository, 
					    |specifies server of it"`
	
	Name string 	`arg:"0" help:"Name of the repository"`
	Version string 	`arg:"1,opt" help:"Version of the repository"`
	Lang string 	`arg:"2,opt" help:"Language of the repository"`
}


func (*selectReposCmd) GetDescriptor() fishly.CommandDescriptor {
	return fishly.CommandDescriptor{
		Name: "select",
		Help: "Shows list of salsa repositories",
		Group: "Repository",
	}
}
func (*selectReposCmd) IsApplicable(ctx *fishly.Context) bool {
	return true		// always can reselect repo
}
func (*selectReposCmd) NewOptions() interface{} {
	return nil		// doesn't support options or arguments
}
func (*selectReposCmd) Complete(ctx *fishly.Context, option string) []string {
	// TODO
	return []string{}
}

func (*selectReposCmd) Execute(ctx *fishly.Context, rq *fishly.Request) error {
	// TODO
	return fmt.Errorf("Not implemented")
}

