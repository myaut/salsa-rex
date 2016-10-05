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
				writeResponseOrError(rw, err, repoKey)
			}
	})
	
	mux.HandleFunc("/repo/taskstatus/", func (rw http.ResponseWriter, rq *http.Request) {
			if parts, ok := parsePath(rq, "taskstatus"); ok {
				// This is request for getting status
				status := salsarex.GetProcessingTaskStatus(parts[0], parts[1])
				writeResponse(rw, status)
			}
	})
	
	return 
}
