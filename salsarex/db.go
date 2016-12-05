package salsarex

import (
	"log"
	
	"sync"
	"reflect"
	
    // arango "gopkg.in/diegogub/aranGO.v2"
    arango "aranGO"
)

var (
	maxBatchSaverWorkers = 32
	batchSaverBatchLen = 32
)

type DBConfig struct {
	URL string
	Username string
	Password string
	Database string
}

// A global pointer to a db session and instance
var dbSession *arango.Session
var db *arango.Database
func InitializeDB(cfg *DBConfig, logDb bool) (err error) {
	arango.SetDefaultDB(cfg.Database)
	
    dbSession, err = arango.Connect(cfg.URL, cfg.Username, cfg.Password, logDb)
    if err != nil {
    	return
    }
    
    db, err = dbSession.CurrentDB()
    return
}

func GetDB() *arango.Database {
	return db
}

// Resets database state -- drops collections and then re-creates it (for debugging :) )
// if fails, will exit server
func ResetDB() {
	for _, collection := range db.Collections {
		db.DropCollection(collection.Name)
	}
	
	// Repository
	db.CreateCollection(arango.NewCollectionOptions("Repository", false))
	
	// File 
	db.CreateCollection(arango.NewCollectionOptions("File", false))
	db.Col("File").CreateHash(false, "Repository")
	db.Col("File").CreateHash(false, "Parent")
	db.Col("File").CreateHash(false, "Path")
	db.Col("File").CreateFullText(3, "Text")
	
	log.Println("Database was recreated")
}


// Cached document -- for documents that are cached in memory but 
// needed to be periodically synced to the database
const (
	cachedNew = iota
	cachedDirty 
	cachedSaved
)

type CachedDocument struct {
	status int
}

func (cd *CachedDocument) Create() {
	cd.status = cachedNew
}
func (cd *CachedDocument) Update() {
	cd.status = cachedDirty
}
func (cd *CachedDocument) Save(col *arango.Collection, doc interface{}) (err error) {
	switch cd.status {
		case cachedNew:
			err = col.Save(doc)
		case cachedDirty:
			// Extract Key from saved arango.Document
			v := reflect.ValueOf(doc)
			f := reflect.Indirect(v).FieldByName("Key")
		
			err = col.Replace(f.String(), doc)
	}
	
	if err == nil {
		cd.status = cachedSaved
	}
	return nil
}

// Batch saver -- spawns worker of goroutines and saves documents in batches 
type batchSaverMessage struct {
	cd *CachedDocument
	doc interface{}
}

type BatchSaver struct {
	colName string
	col *arango.Collection
	
	wg sync.WaitGroup
	
	channel chan batchSaverMessage
} 

func NewBatchSaver(colName string) (*BatchSaver) {
	bs := new(BatchSaver)
	bs.colName = colName
	bs.col = db.Col(colName)
	bs.channel = make(chan batchSaverMessage, maxBatchSaverWorkers)
	
	for i := 0 ; i < maxBatchSaverWorkers ; i++ {
		go batchSaverRoutine(bs)
	}
	
	return bs
}

func (bs *BatchSaver) AddDocument(cd *CachedDocument, doc interface{}) {
	if cd.status == cachedSaved {
		return
	}
	
	bs.wg.Add(1)
	bs.channel <- batchSaverMessage{
		cd: cd,
		doc: doc,
	}
}

func (bs *BatchSaver) Wait() {
	bs.wg.Wait()
}

func (bs *BatchSaver) Complete() {
	for i := 0 ; i < maxBatchSaverWorkers ; i++ {
		bs.channel <- batchSaverMessage{}
	}
}

func batchSaverRoutine(bs* BatchSaver) {
	batch := make([]interface{}, 0, batchSaverBatchLen)
	
	for {
		msg := <- bs.channel
		if len(batch) == batchSaverBatchLen || msg.cd == nil {
			bs.col.BatchSave(batch)
			batch = make([]interface{}, 0, batchSaverBatchLen)
		
			if msg.cd == nil {
				return
			}
		}
		
		batch = append(batch, msg.doc)
		bs.wg.Done()
	}
}
