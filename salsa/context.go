package main

import (
	"fmt"
	
	"strings"
	
	"fishly"
	"salsacore/client"
)

const (
	pathRepoKey = iota
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

func (ctx *SalsaContext) Update(cliCtx *fishly.Context) {
	path := cliCtx.GetCurrentState().Path
	vars := cliCtx.GetCurrentState().Variables
	
	if len(path) < lengthPathRepo {
		cliCtx.Prompt = ""
		ctx.handle.ResetActiveRepository()
	} else {
		repoPrompt := fmt.Sprintf("%s-%s", path[pathRepoName], 
				path[pathRepoVersion])
		
		for _, objectKey := range []string{"searchKey"} {
			if value, ok := vars[objectKey]; ok {
				cliCtx.Prompt = fmt.Sprintf("%s?%s=%s", repoPrompt, objectKey, value)
				return
			}
		}
		
		cliCtx.Prompt = fmt.Sprintf("%s:/%s", repoPrompt, 
				strings.Join(path[lengthPathRepo:], "/"))
	}
}

func (ctx *SalsaContext) Cancel(rq *fishly.Request) {
	ctx.handle.Cancel(rq.Id)
}
