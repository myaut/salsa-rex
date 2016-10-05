package main

import (
	"net/http"
	"encoding/json"
	
	"strings"
	"strconv"
)

func parsePath(rq *http.Request, anchor string) ([]string, bool) {		
	parts := strings.Split(rq.URL.Path, "/")
	for i, part := range parts {
		if part == anchor {
			return parts[i+1:], true
		}
	}
	
	return nil, false
}
func tryParsePathString(rq *http.Request, anchor string) (string, bool) {
	parts, ok := parsePath(rq, anchor)
	if !ok || len(parts) != 1  {
		return "", false
	}
	
	return parts[0], true
}
func tryParsePathInt(rq *http.Request, anchor string) (int, bool) {
	parts, ok := parsePath(rq, anchor)
	if !ok || len(parts) != 1  {
		return -1, false
	}
	
	i, err := strconv.Atoi(parts[0])
	if err != nil {
		return -1, false
	}
	
	return i, true
}

// Helpers for processing JSON 
func readPostData(rw http.ResponseWriter, rq *http.Request, value interface{}) bool {
	if rq.Method != "POST" {
		http.Error(rw, "Method not allowed", 405)
		return false
	}
	if rq.Body == nil {
        http.Error(rw, "Please send a request body", 400)
        return false
    }
    err := json.NewDecoder(rq.Body).Decode(value)
    if err != nil {
        http.Error(rw, err.Error(), 400)
        return false
    }
    
    return true
}

func writeResponse(rw http.ResponseWriter, value interface{}) {
	json.NewEncoder(rw).Encode(value)
}

func writeResponseOrError(rw http.ResponseWriter, err error, value interface{}) {
	if err != nil {
		http.Error(rw, err.Error(), 400)
	} else {
		writeResponse(rw, value)
	}
}

