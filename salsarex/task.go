package salsarex

import (
	"os"
	"log"
	"strings"
	"path/filepath"
	"sync"
	"sync/atomic"
	
	"salsacore"
)

type RepositoryParseTask struct {
	Repository
	
	taskType string
	
	// Total files in this repository parse request. If set to 0
	// then we didn't complete walking over it
	totalFiles int32 
	
	// Amount of files that are already parsed 
	parsedFiles int32
}

type RepositoryParseTaskTable map[string]*RepositoryParseTask

// Global map of repository parsing tasks
var (
	mu sync.RWMutex
	taskMap RepositoryParseTaskTable = make(RepositoryParseTaskTable)
)

// Create repository and submit parsing task and returns its Id in repo.RepoId
func CreateParseTask(repo *salsacore.Repository) (repoKey string, err error) {
	// TODO: preliminary checks
	
	obj := NewRepository(repo)
	
	err = obj.Save()
	if err == nil {
		repoKey = obj.Document.Key
		task := createParseTask(obj, "parse")
		go walkRepository(task)
	}
	
	return
}

// Returns parsed/total values or -1,-1 if parsing/processing task not found
func GetParsingTaskStatus(taskType, repoKey string) (status salsacore.RepositoryParsingStatus) {
	mu.RLock()
	defer mu.RUnlock()
	
	task, ok := taskMap[taskType + ":" + repoKey]
	if ok {
		status.Parsed = atomic.LoadInt32(&task.parsedFiles)
		status.Total = atomic.LoadInt32(&task.totalFiles)
	} else {
		status.Total = -1
	}
	
	return
}

func createParseTask(repo *Repository, taskType string) *RepositoryParseTask {
	mu.Lock()
	defer mu.Unlock()
	
	task := new(RepositoryParseTask)
	task.Repository = *repo
	task.taskType = taskType
	
	taskMap[taskType + ":" + repo.Key] = task
	
	return task
}

// Walks repository and submits parsing/lexing jobs
func walkRepository(task *RepositoryParseTask) {
	validExtensions := getValidExtensions(task)
	var totalFiles int32 = 0
	
	filepath.Walk(task.Repository.Path, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		
		if hasValidExtension(path, validExtensions) {
			path = strings.TrimPrefix(path, task.Repository.Path)
			
			file := NewRepositoryFile(&task.Repository, path) 			
			totalFiles += 1
			
			go parseFile(task, file)
		} 
		return nil
	})
	
	if totalFiles == 0 {
		// Oops, nothing was found
		deleteParsingTask(task)
	} else {
		atomic.AddInt32(&task.totalFiles, totalFiles)
	}
}

func getValidExtensions(task *RepositoryParseTask) []string {
	validExtensions := make([]string, 0, 10)
	lang := task.Repository.Lang 
	
	if lang == "C" || lang == "CPP" {
		validExtensions = append(validExtensions, ".c", ".h")
		if lang == "CPP" {
			validExtensions = append(validExtensions, ".cpp", ".hpp", ".C", ".cxx")
		}
	} else if lang == "JAVA" {
		validExtensions = append(validExtensions, ".java")
	}  
	
	return validExtensions
}

func hasValidExtension(path string, validExtensions []string) bool {
	for _, extension := range validExtensions {
		if strings.HasSuffix(path, extension) {
			return true
		}
	}
	
	return false
}

func parseFile(task *RepositoryParseTask, file *RepositoryFile) {
	defer finishParsingFile(task)
	
	// Open file
	absPath := filepath.Join(task.Repository.Path, file.Path)
	f, err := os.Open(absPath)
	if err != nil {
		log.Printf("Error parsing %s: %v", absPath, err)
		return
	}
	
	// Tokenize
	var l Lexer
	l.Init(f, file.Path, task.Repository.Lang)
	for t := l.LexScan() ; t.Type != salsacore.EOF ; t = l.LexScan() {
		file.Tokens = append(file.Tokens, t)
	}
	
	err = file.Save()
	if err != nil {
		log.Printf("Error parsing %s: %v", absPath, err)
		return		
	}
	
	// TODO: when completed, run indexers on it
}

func finishParsingFile(task* RepositoryParseTask) {
	parsedFiles := atomic.AddInt32(&task.parsedFiles, 1)
	if parsedFiles == atomic.LoadInt32(&task.totalFiles) {
		deleteParsingTask(task)
	}
}

func deleteParsingTask(task *RepositoryParseTask) {
	// this was last file to be parsed -- request is satisfied
	mu.Lock()
	defer mu.Unlock()
	
	delete(taskMap, task.taskType + ":" + task.Repository.Key)
}
