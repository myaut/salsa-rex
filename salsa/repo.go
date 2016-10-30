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

type selectRepoCmd struct {
}
type selectRepoOpt struct {
	Server string `opt:"server|s,opt"`
	
	Name string 	`arg:"1"`
	Version string 	`arg:"2,opt"`
	Lang string 	`arg:"3,opt"`
}

func (*selectRepoCmd) IsApplicable(ctx *fishly.Context) bool {
	return true		// always can reselect repo
}
func (*selectRepoCmd) NewOptions() interface{} {
	return new(selectRepoOpt)
}
func (*selectRepoCmd) Complete(ctx *fishly.Context, option string) []string {
	// TODO
	return []string{}
}

func (*selectRepoCmd) Execute(ctx *fishly.Context, rq *fishly.Request) error {
	// TODO
	return fmt.Errorf("Not implemented")
}

