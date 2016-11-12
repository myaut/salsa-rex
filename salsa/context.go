package main

import (
	"fishly"
	"salsacore/client"
)

type SalsaContext struct {
	handle *client.Handle
}

func NewSalsaContext() (*SalsaContext) {
	ctx := new(SalsaContext)
	ctx.handle = client.NewHandle()
	
	return ctx
}

func (ctx *SalsaContext) Cancel(rq *fishly.Request) {
	ctx.handle.Cancel(rq.Id)
}
