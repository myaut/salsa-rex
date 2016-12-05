package main

import (
	"fmt"
	
	"strings"
	
	"fishly"
	"salsacore/client"
)

const (
	pathServerName = iota
	pathRepoKey
	pathRepoName
	pathRepoVersion
	pathRepoLang
	lengthPathRepo
)

type SalsaContext struct {
	handle *client.Handle
}

func NewSalsaContext() (*SalsaContext) {
	ctx := new(SalsaContext)
	ctx.handle = client.NewHandle()
	
	return ctx
}

func (ctx *SalsaContext) Update(cliCtx *fishly.Context) error {
	path := cliCtx.GetCurrentState().Path
	vars := cliCtx.GetCurrentState().Variables
	
	if len(path) < lengthPathRepo {
		if len(path) != 0 {
			return fmt.Errorf("Invalid path '%s', missing components", 
					strings.Join(path, fishly.PathSeparator))	
		}
		
		cliCtx.Prompt = ""
		ctx.handle.ResetActiveRepository()
		
		return nil
	}
	
	// Generate prompt
	repoPrompt := fmt.Sprintf("%s-%s", path[pathRepoName], 
			path[pathRepoVersion])
	hasVar := false
	for _, objectKey := range []string{"searchKey"} {
		if value, ok := vars[objectKey]; ok {
			cliCtx.Prompt = fmt.Sprintf("%s (%s %s)", repoPrompt, objectKey, value)
			hasVar = true
		}
	}
	if !hasVar {
		cliCtx.Prompt = fmt.Sprintf("%s:/%s", repoPrompt, 
				strings.Join(path[lengthPathRepo:], fishly.PathSeparator))
	}
	
	// If we selected new active repository, validate selection
	actServer, actRepo := ctx.handle.GetActiveRepositoryKeys()
	if actServer != path[pathServerName] || actRepo != path[pathRepoKey] {
		err := ctx.handle.SelectActiveRepositoryEx(path[pathServerName],
				path[pathRepoKey])
		if err != nil {
			return err
		}
		
		// TODO: check repository existance (need hctx.GetRepository under deadline) 
	}
	return nil
}

func (ctx *SalsaContext) Cancel(rq *fishly.Request) {
	ctx.handle.Cancel(rq.Id)
}
