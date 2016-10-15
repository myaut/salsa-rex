package salsarex

import (
	"os"
	"fmt"
	"log"
	"strings"
	"path/filepath"
	"sync"
	"sync/atomic"
	
	"salsacore"
	
	"aranGO/aql"
)

type RepositoryProcessingTask struct {
	Repository
	
	taskType string
	
	// Total files in this repository parse request. If set to 0
	// then we didn't complete walking over it
	totalFiles int32 
	
	// Amount of files that are already parsed/processed by indexers 
	processedFiles int32
	
	totalIndexers int32
	completedIndexers int32
	
	// List of indexers to run
	indexers []Indexer
	
	// Semaphore to regulate number of running parser/indexer routines
	routinesChannel chan int32
}

type repositoryProcessingTaskTable map[string]*RepositoryProcessingTask

// Global map of repository parsing tasks
var (
	mu sync.RWMutex
	taskMap = make(repositoryProcessingTaskTable)
	maxProcessingRoutines = 768
)

func SetMaxProcessingRoutines(value int) {
	maxProcessingRoutines = value
}

// Create repository and submit parsing task and returns its Id in repo.RepoId
func CreateParseTask(repo *salsacore.Repository) (repoKey string, err error) {
	// TODO: preliminary checks
	
	obj := NewRepository(repo)
	
	err = db.Col("Repository").Save(obj)
	if err == nil {
		repoKey = obj.Document.Key
		task := createProcessingTask(obj, "parse")
		task.indexers, err = createIndexers(repo.Lang, repo.Indexers, []string{})
		
		if err == nil {
			err = startIndexing(task)
			if err == nil {
				go walkRepository(task)
			}
		}
	}
	
	return
}

func CreateIndexingTask(repoKey string, indexer string) (err error) {
	var repo Repository
	
	err = db.Col("Repository").Get(repoKey, &repo)
	if err != nil {
		return fmt.Errorf("Unknown repository %s: %v", repoKey, err)
	}
	
	task := createProcessingTask(&repo, indexer)
	task.indexers, err = createIndexers(repo.Lang, []string{indexer}, repo.Indexers)
	if err == nil {
		// Add names of created indexers to repository & save it
		for _, indexer := range task.indexers {
			repo.Indexers = append(repo.Indexers, indexer.GetFactory().GetName()) 
		}
		db.Col("Repository").Replace(repo.Key, &repo)
		
		err = startIndexing(task)
		if err == nil {
			go indexRepository(task)
		}
	}
	
	return
}

// Returns parsed/total values or -1,-1 if parsing/processing task not found
func GetProcessingTaskStatus(repoKey, taskType string) (status salsacore.RepositoryProcessingStatus) {
	mu.RLock()
	defer mu.RUnlock()
	
	task, ok := taskMap[taskType + ":" + repoKey]
	if ok {
		status.Processed = atomic.LoadInt32(&task.processedFiles)
		status.Total = atomic.LoadInt32(&task.totalFiles)
		status.Indexers = atomic.LoadInt32(&task.totalIndexers)
	} else {
		status.Total = -1
	}
	
	return
}

func createProcessingTask(repo *Repository, taskType string) *RepositoryProcessingTask {
	mu.Lock()
	defer mu.Unlock()
	
	task := new(RepositoryProcessingTask)
	task.Repository = *repo
	task.taskType = taskType
	task.routinesChannel = make(chan int32, maxProcessingRoutines)
	
	taskMap[taskType + ":" + repo.Key] = task
	
	return task
}

// Walks repository and submits parsing/lexing jobs
func walkRepository(task *RepositoryProcessingTask) {
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
	
	setTotalFiles(task, totalFiles)
}

func getValidExtensions(task *RepositoryProcessingTask) []string {
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

func parseFile(task *RepositoryProcessingTask, file *RepositoryFile) {
	task.routinesChannel <- 1
	defer finishProcessingFile(task)
	
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
	// TODO: estimate number of tokens based on file size
	file.Tokens = make([]salsacore.Token, 0, 1000)
	for t := l.LexScan() ; t.Type != salsacore.EOF ; t = l.LexScan() {
		file.Tokens = append(file.Tokens, t)
	}
	
	err = f.Close()
	if err == nil {
		err = db.Col("File").Save(file)
	}
	if err != nil {
		log.Printf("Error parsing %s: %v", absPath, err)
		return		
	}
	
	runIndexers(task, file)
}

func startIndexing(task *RepositoryProcessingTask) error {
	for i, indexer := range task.indexers {
		atomic.AddInt32(&task.totalIndexers, 1)
		err := indexer.StartIndexing(&task.Repository)
		if err != nil {
			// rollback started indexers
			for j := i ; j >= 0 ; j -= 1 {
				atomic.AddInt32(&task.totalIndexers, -1)
				task.indexers[j].FinishIndexing()
			}
			
			return err
		}
	}
	
	return nil
}

func indexRepository(task *RepositoryProcessingTask) error {
	q := aql.NewQuery(`
		FOR f in File
		FILTER f.Repository == @Repository 
		RETURN f`)
	q.AddBind("Repository", task.Repository.Key)
	
	cur, err := db.Execute(q)
	if err != nil {
		setTotalFiles(task, 0)
		return err
	}
	
	setTotalFiles(task, int32(cur.Count()))
	
	file := new(RepositoryFile)
	for cur.FetchOne(file) {
		go indexFile(task, file)
		file = new(RepositoryFile)
	}
	return nil
}

func indexFile(task *RepositoryProcessingTask, file *RepositoryFile) {
	task.routinesChannel <- 1
	defer finishProcessingFile(task)
	
	runIndexers(task, file)
}

func runIndexers(task *RepositoryProcessingTask, file *RepositoryFile) {
	for _, indexer := range task.indexers {
		indexer.CreateIndex(file)
	}
}

func setTotalFiles(task* RepositoryProcessingTask, totalFiles int32) {
	if totalFiles == 0 {
		// Oops, nothing was found
		deleteProcessingTask(task)
	} else {
		atomic.AddInt32(&task.totalFiles, totalFiles)
	}
}

func finishProcessingFile(task* RepositoryProcessingTask) {
	processedFiles := atomic.AddInt32(&task.processedFiles, 1)
	
	if processedFiles == atomic.LoadInt32(&task.totalFiles) {
		for _, indexer := range task.indexers {
			indexer.FinishIndexing()
			atomic.AddInt32(&task.totalIndexers, -1)
		}
		
		deleteProcessingTask(task)
	} else {
		<- task.routinesChannel
	}
}

func deleteProcessingTask(task *RepositoryProcessingTask) {
	// this was last file to be parsed -- request is satisfied
	mu.Lock()
	defer mu.Unlock()
	
	delete(taskMap, task.taskType + ":" + task.Repository.Key)
}
