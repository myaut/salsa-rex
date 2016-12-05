package salsarex

import (
	"fmt"
	"log"
	"os"

	"bufio"
	"bytes"
	"io"
	"strconv"
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

	validExtensions []string

	// Total files in this repository parse request. If set to 0
	// then we didn't complete walking over it
	totalFiles int32

	// Amount of files that are already parsed/processed by indexers
	processedFiles int32

	totalIndexers     int32
	completedIndexers int32

	// List of indexers to run
	indexers []Indexer

	// Semaphore to regulate number of running parser/indexer routines
	routinesChannel chan int32
}

type repositoryProcessingTaskTable map[string]*RepositoryProcessingTask

// Global map of repository parsing tasks
var (
	mu                    sync.RWMutex
	taskMap               = make(repositoryProcessingTaskTable)
	maxProcessingRoutines = 768
)

var textCharTable = []bool{
	false, false, false, false, false, false, false, true,
	true,  true,  true,  false, true,  true,  false, false,
	false, false, false, false, false, false, false, false, 
	false, false, false, true,  false, false, false, false,
}

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
		task.setValidExtensions()
		task.indexers, err = createIndexers(repo.Lang, repo.Indexers, []string{})

		if err == nil {
			err = task.startIndexing()
			if err == nil {
				go task.walkRepository()
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

		err = task.startIndexing()
		if err == nil {
			go task.indexRepository()
		}
	}

	return
}

// Returns parsed/total values or -1,-1 if parsing/processing task not found
func GetProcessingTaskStatus(repoKey, taskType string) (status salsacore.RepositoryProcessingStatus) {
	mu.RLock()
	defer mu.RUnlock()

	task, ok := taskMap[taskType+":"+repoKey]
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

	taskMap[taskType+":"+repo.Key] = task

	return task
}

// Walks repository and submits parsing/lexing jobs
func (task *RepositoryProcessingTask) walkRepository() {
	var totalFiles int32 = 0
	parents := make(map[string]string)

	filepath.Walk(task.Repository.Path, func(path string, info os.FileInfo, err error) error {
		path = strings.TrimPrefix(path, task.Repository.Path)
		file := NewRepositoryFile(&task.Repository, path, info)
		file.Parent, _ = parents[filepath.Dir(path)]

		if info.IsDir() {
			parents[path] = file.Key
			task.saveFile(file)
			return nil
		}

		totalFiles += 1

		go task.processFile(file)
		return nil
	})

	task.setTotalFiles(totalFiles)
}

func (task *RepositoryProcessingTask) setValidExtensions() {
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

	task.validExtensions = validExtensions
}

func (task *RepositoryProcessingTask) hasValidExtension(path string) bool {
	for _, extension := range task.validExtensions {
		if strings.HasSuffix(path, extension) {
			return true
		}
	}

	return false
}

func (task *RepositoryProcessingTask) processFile(file *RepositoryFile) {
	task.routinesChannel <- 1
	defer task.finishProcessingFile()

	absPath := filepath.Join(task.Repository.Path, file.Path)
	f, err := os.Open(absPath)
	if err != nil {
		log.Printf("Error opening %s: %v", absPath, err)
		return
	}
	defer f.Close()
	
	// Determine if this is a text file -- read first 512 byte and ensure
	// that all runes are printable
	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		log.Printf("Error reading %s: %v", absPath, err)
		return
	}

	buf := bytes.NewBuffer(header[:n])
	for {
		r, _, err := buf.ReadRune()
		if err == io.EOF {
			break
		}
		
		if err != nil {
			log.Printf("Error reading %s: header error %s", absPath, r, err)
			// If some error occured, this file is not readable, treat it
			// as other file
			task.saveFile(file)
			return
		}
		
		if (r < 0x20 && textCharTable[r]) {
			continue
		}
		if !strconv.IsPrint(r) {
			log.Printf("Error reading %s: non-printable rune %d", absPath, r)
			task.saveFile(file)
			return
		}
	}

	file.FileType = salsacore.RFTText
	f.Seek(0, 0)
	task.processTextFile(file, f)
}

func (task *RepositoryProcessingTask) processTextFile(file *RepositoryFile, f *os.File) {
	buf := bufio.NewReader(f)
	for {
		line, err := buf.ReadString(byte('\n'))
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Error reading %s: %v", file.Path, err)
			return
		}

		line = strings.TrimRight(line, "\n")
		file.Lines = append(file.Lines, line)
	}

	if !task.hasValidExtension(file.Name) {
		task.saveFile(file)
		return
	}

	file.FileType = salsacore.RFTSource
	f.Seek(0, 0)
	task.parseFile(file, f)
}

func (task *RepositoryProcessingTask) saveFile(file *RepositoryFile) {
	// Prepare file's text fo indexing
	buf := bytes.NewBuffer([]byte{})
	if len(file.Tokens) > 0 {
		for _, token := range file.Tokens {
			buf.WriteString(token.Text)
			buf.WriteRune(' ')
		}
	} else if len(file.Lines) > 0 {
		for _, line := range file.Lines {
			// TODO: preprocess line, i.e. remove punctuation
			buf.WriteString(line)
			buf.WriteRune(' ')
		}
	}
	file.Text = string(buf.Bytes())

	err := db.Col("File").Save(file)
	if err != nil {
		log.Printf("Error saving file %s: %v", file.Path, err)
	}

	// log.Printf("Added file %s of type #%d", file.Path, file.FileType)
}

func (task *RepositoryProcessingTask) parseFile(file *RepositoryFile, f *os.File) {
	// Tokenize
	var l Lexer
	l.Init(f, file.Path, task.Repository.Lang)
	// TODO: estimate number of tokens based on file size
	file.Tokens = make([]salsacore.Token, 0, 1000)
	for t := l.LexScan(); t.Type != salsacore.EOF; t = l.LexScan() {
		file.Tokens = append(file.Tokens, t)
	}

	task.saveFile(file)
	task.runIndexers(file)
}

func (task *RepositoryProcessingTask) startIndexing() error {
	for i, indexer := range task.indexers {
		atomic.AddInt32(&task.totalIndexers, 1)
		err := indexer.StartIndexing(&task.Repository)
		if err != nil {
			// rollback started indexers
			for j := i; j >= 0; j -= 1 {
				atomic.AddInt32(&task.totalIndexers, -1)
				task.indexers[j].FinishIndexing()
			}

			return err
		}
	}

	return nil
}

func (task *RepositoryProcessingTask) indexRepository() error {
	q := aql.NewQuery(`
		FOR f in File
		FILTER f.Repository == @Repository 
		RETURN f`)
	q.AddBind("Repository", task.Repository.Key)

	cur, err := db.Execute(q)
	if err != nil {
		task.setTotalFiles(0)
		return err
	}

	task.setTotalFiles(int32(cur.Count()))

	file := new(RepositoryFile)
	for cur.FetchOne(file) {
		go task.indexFile(file)
		file = new(RepositoryFile)
	}
	return nil
}

func (task *RepositoryProcessingTask) indexFile(file *RepositoryFile) {
	task.routinesChannel <- 1
	defer task.finishProcessingFile()

	task.runIndexers(file)
}

func (task *RepositoryProcessingTask) runIndexers(file *RepositoryFile) {
	for _, indexer := range task.indexers {
		indexer.CreateIndex(file)
	}
}

func (task *RepositoryProcessingTask) setTotalFiles(totalFiles int32) {
	if totalFiles == 0 {
		// Oops, nothing was found
		deleteProcessingTask(task)
	} else {
		atomic.AddInt32(&task.totalFiles, totalFiles)
	}
}

func (task *RepositoryProcessingTask) finishProcessingFile() {
	processedFiles := atomic.AddInt32(&task.processedFiles, 1)

	if processedFiles == atomic.LoadInt32(&task.totalFiles) {
		for _, indexer := range task.indexers {
			indexer.FinishIndexing()
			atomic.AddInt32(&task.totalIndexers, -1)
		}

		deleteProcessingTask(task)
	} else {
		<-task.routinesChannel
	}
}

func deleteProcessingTask(task *RepositoryProcessingTask) {
	// this was last file to be parsed -- request is satisfied
	mu.Lock()
	defer mu.Unlock()

	delete(taskMap, task.taskType+":"+task.Repository.Key)
}
