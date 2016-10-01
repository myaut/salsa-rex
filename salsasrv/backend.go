package main

import (
	"net/http"
	
	"salsacore"
	"salsarex"
)

// "main" function which registers all handlers
func createBackendAPIHandlers() (mux *http.ServeMux) {
	mux = http.NewServeMux()
	
	// for debugging
	mux.HandleFunc("/resetdb", func (rw http.ResponseWriter, rq *http.Request) {
			salsarex.ResetDB()
			rw.Write([]byte("DB has been prepared!"))
	})
	mux.HandleFunc("/repo", func (rw http.ResponseWriter, rq *http.Request) {
			// This is POST request for submitting new repository to parse
			var repo salsacore.Repository
			if readPostData(rw, rq, &repo) {
				repoKey, err := salsarex.CreateParseTask(&repo)
				if err != nil {
					http.Error(rw, err.Error(), 400)
				} else {
					writeResponse(rw, repoKey)
				}
			}
	})
	mux.HandleFunc("/repo/taskstatus/", func (rw http.ResponseWriter, rq *http.Request) {
			if parts, ok := parsePath(rq, "repo"); ok {
				// This is request for getting status
				status := salsarex.GetParsingTaskStatus(parts[0], parts[1])
				writeResponse(rw, status)
			}
	})
	
	return 
}
