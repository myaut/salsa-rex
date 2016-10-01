package main

import (
	"net/http"
	
	"salsarex"
)

// "main" function which registers all handlers
func createFrontendAPIHandlers() (mux *http.ServeMux) {
	mux = http.NewServeMux()
	
	mux.HandleFunc("/tokens/", func (rw http.ResponseWriter, rq *http.Request) {
			if fileId, ok := tryParsePathInt(rq, "tokens"); ok {
				// /tokens/fid -> list of tokens  
				tokens := salsarex.GetTokens(fileId)
				writeResponse(rw, tokens)
			}
	})
	
	return 
}
