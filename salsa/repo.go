package main 

import (
	"fmt"
	"log"
	
	"fishly"
	
	"salsacore"
	"salsacore/client"
)

// 
// ls command for repositories -- shows list of repositories
// 

type listReposCmd struct {
}
type listReposOpt struct {
	Server string 	`opt:"server|s,opt"`
	
	Name string 	`arg:"1,opt"`
}

func (*listReposCmd) IsApplicable(ctx *fishly.Context) bool {
	return len(ctx.GetCurrentState().Path) == 0	// only applicable when no repository is picked
}

func (*listReposCmd) NewOptions() interface{} {
	return new(listReposOpt)
}

func (*listReposCmd) Complete(ctx *fishly.Context, rq *fishly.CompleterRequest) {
	salsaCtx := ctx.External.(*SalsaContext)
	
	switch rq.ArgIndex {
		case 0:
			switch rq.Option {
				case "server":
					for _, srv := range salsaCtx.handle.Servers {
						rq.AddOption(srv.Name)
					}
			}
		// case 1: -- repository names 
	}
}

func (cmd *listReposCmd) Execute(ctx *fishly.Context, rq *fishly.Request) error {
	salsaCtx := ctx.External.(*SalsaContext)
	options := rq.Options.(*listReposOpt)
	
	_, err := cmd.findRepositories(salsaCtx, rq.Id, -1, &salsacore.Repository {
		Name: options.Name,
	})
	if err != nil {
		return err
	}
	
	return fmt.Errorf("Not implemented")
}

func (*listReposCmd) findRepositories(salsaCtx *SalsaContext, requestId, useServer int,
			repo *salsacore.Repository) ([]client.ServerRepository, error) {
	repos := make([]client.ServerRepository, 0)
	
	for serverIndex, server := range salsaCtx.handle.Servers {
		if useServer >= 0 && serverIndex != useServer {
			continue
		}
		
		hctx, err := salsaCtx.handle.NewServerContext(requestId, serverIndex)
		if err != nil {
			return nil, err
		}
		defer hctx.Done()
		
		srvRepos, err := hctx.FindRepositories(repo) 
		if err != nil {
			log.Printf("Error fetching list of repositories from %s: %v", server.Name, err)
			continue
		}
		
		repos = append(repos, srvRepos...)
	}
	
	return repos, nil
}


// 
// Select an active repository using select command
// 

type selectRepoCmd struct {
	listReposCmd
}
type selectRepoOpt struct {
	Server string 	`opt:"server|s,opt"`
	
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
func (cmd *selectRepoCmd) Complete(ctx *fishly.Context, rq *fishly.CompleterRequest) {
	cmd.listReposCmd.Complete(ctx, rq)
}

func (*selectRepoCmd) Execute(ctx *fishly.Context, rq *fishly.Request) error {
	// TODO
	return fmt.Errorf("Not implemented")
}

