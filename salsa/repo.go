package main 

import (
	"fmt"
	"log"
	
	"time"
	
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

func (cmd *listReposCmd) Complete(ctx *fishly.Context, rq *fishly.CompleterRequest) {
	salsaCtx := ctx.External.(*SalsaContext)
	
	switch rq.ArgIndex {
		case 0:
			switch rq.Option {
				case "server":
					for _, srv := range salsaCtx.handle.Servers {
						rq.AddOption(srv.Name)
					}
			}
		case 1:
			repos, _ := cmd.findRepositories(salsaCtx, rq.Id, "", 
				rq.GetDeadline(), &salsacore.Repository{})
			for _, repo := range repos {
				rq.AddOption(repo.Name)
			}
	}
}

func (cmd *listReposCmd) Execute(ctx *fishly.Context, rq *fishly.Request) (err error) {
	salsaCtx := ctx.External.(*SalsaContext)
	options := rq.Options.(*listReposOpt)
	
	repos, err := cmd.findRepositories(salsaCtx, rq.Id, options.Server, time.Time{},
		&salsacore.Repository {
			Name: options.Name,
		})
	if err != nil {
		return
	}
	
	ioh, err := rq.StartOutput(ctx, false)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()
	
	ioh.StartArray("repositories")
	for _, repo := range repos {
		ioh.StartObject("repository")
		
		ioh.WriteString("server", repo.Server)
		ioh.WriteString("key", repo.Key)
		ioh.WriteString("name", repo.Name)
		ioh.WriteString("version", repo.Version)
		ioh.WriteString("lang", repo.Lang)
		
		ioh.EndObject()
	}
	ioh.EndArray()
	
	return
}

func (*listReposCmd) findRepositories(salsaCtx *SalsaContext, requestId int,
			serverName string, deadline time.Time, repo *salsacore.Repository) ([]client.ServerRepository, error) {
	repos := make([]client.ServerRepository, 0)
	foundServer := false
	
	for serverIndex, server := range salsaCtx.handle.Servers {
		if len(serverName) > 0 && server.Name != serverName {
			if foundServer {
				break
			}
			continue
		} else {
			foundServer = true
		}
		
		hctx, err := salsaCtx.handle.NewServerContext(requestId, serverIndex)
		if err != nil {
			return repos, err
		}
		defer hctx.Done()
		hctx.WithDeadline(deadline)
		
		// Try to use name as repository Key
		srvRepo, err := hctx.GetRepository(repo.Name)
		if err == nil {
			repos = append(repos, srvRepo)
			continue
		}
		
		srvRepos, err := hctx.FindRepositories(repo) 
		if err != nil {
			log.Printf("Error fetching list of repositories from %s: %v", server.Name, err)
			continue
		}
		
		repos = append(repos, srvRepos...)
	}
	
	if !foundServer {
		return nil, fmt.Errorf("Not found server '%s'", serverName)
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

func (cmd *selectRepoCmd) Execute(ctx *fishly.Context, rq *fishly.Request) error {
	salsaCtx := ctx.External.(*SalsaContext)
	options := rq.Options.(*selectRepoOpt)
	
	repos, err := cmd.findRepositories(salsaCtx, rq.Id, options.Server, time.Time{}, 
		&salsacore.Repository {
			Name: options.Name,
			Version: options.Version,
			Lang: options.Lang,
		})
	if err != nil {
		return err
	}
	
	if len(repos) == 0 {
		return fmt.Errorf("Repository '%s' is not found", options.Name)
	}
	
	// Select repository with most recent version
	repo := repos[0]
	for _, other := range repos {
		if repo.Lang != other.Lang {
			return fmt.Errorf("Ambiguity: multiple repositories with different languages found")	
		}
		if repo.SemverCompare(other.Repository) > 0 {
			repo = other
		}
	}
	
	state := ctx.PushState(true)
	state.Path = []string{repo.Key, repo.Name, repo.Version, repo.Lang}
	salsaCtx.handle.SelectActiveRepository(repo)
	
	return nil
}

