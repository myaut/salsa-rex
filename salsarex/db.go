package salsarex

import (
	"log"
	
    // arango "gopkg.in/diegogub/aranGO.v2"
    arango "aranGO"
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
func InitializeDB(cfg *DBConfig) (err error) {
	arango.SetDefaultDB(cfg.Database)
	
    dbSession, err = arango.Connect(cfg.URL, cfg.Username, cfg.Password, false)
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
	db.Col("File").CreateHash(false, "Path")
	
	log.Println("Database was recreated")
}
