package main

import (
	"os"
	"flag"
	"log"
	"errors"
	"strings"
	
	"fmt"
	
	"salsacore"
	
	"time"
)

func main() {
    server := flag.String("server", "http://localhost:80/api", "server API URI")
    
    // action flags
    resetDb := flag.Bool("reset-db", false, "reset database")
    createRepo := flag.Bool("create-repo", false, `create repository:
		salsactl -create-repo Path=<path> Name=<name> Version=<version> Lang=<C|CPP|JAVA>`)
    flag.Parse()
    
    var err error
    client := salsacore.NewClient(*server)
    
    switch true {
	case *resetDb:
    	err = client.Get("/resetdb")
	case *createRepo:
		var repo salsacore.Repository
		var repoKey string
		var completed bool = false
	    
	    err = client.PostArguments("/repo", &repo)
	    if err == nil {
	    	repoKey, err = client.DecodeObjectKey()
	    	fmt.Printf("Repository #%s created\n", repoKey)
	    }
	    for ; err == nil && !completed ;  {
	    	err, completed = pollRepoStatus(client, "parse", repoKey)
	    }
	    fmt.Println("")
    default:
	    err = errors.New("No option specified")
    }
    
    if err != nil {
    	client.CopyResponse(os.Stderr)
    	
    	log.Fatalln(err)
    }
}

func clearLine() {
	fmt.Print("\r", strings.Repeat(" ", 80), "\r")
}

func pollRepoStatus(client *salsacore.Client, taskType, repoKey string) (error, bool) {
	var status salsacore.RepositoryProcessingStatus
	
	err := client.GetValue("/repo/" + repoKey + "/taskstatus/" + taskType, &status)
	if err != nil {
		return err, true
	}
	    	
	clearLine()
	switch status.Total {
		case -1:
    		fmt.Printf("Done")
    		return nil, true
    	case 0:
	    	fmt.Printf("Processed %d sources", status.Processed)
	    	time.Sleep(1 * time.Second)
    	default:
	    	fmt.Printf("Processed %d/%d sources (%d indexers)", status.Processed, status.Total, status.Indexers)
	    	time.Sleep(500 * time.Millisecond)
	}
	
	return nil, false
}
