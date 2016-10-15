package main

import (
	_ "salsarex"
	
	"github.com/labstack/echo"
)

// "main" function which registers all handlers
func createFrontendAPIHandlers(prefix string, server *echo.Echo) {
	/*
	server.GET(prefix + "/tokens/", func (ctx echo.Context) {
			if fileId, ok := tryParsePathInt(rq, "tokens"); ok {
				// /tokens/fid -> list of tokens  
				tokens := salsarex.GetTokens(fileId)
				writeResponse(rw, tokens)
			}
			
			
	})
	*/
}
