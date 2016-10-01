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

var collections = []string {
	"Repository",
	"File",
	"Identifier",
}

// Resets database state -- drops collections and then re-creates it (for debugging :) )
// if fails, will exit server
func ResetDB() {
	var err error 
	
	for _, name := range collections {
		if db.ColExist(name) {
			db.DropCollection(name)
		}
		
		err = db.CreateCollection(arango.NewCollectionOptions(name, false))
		if err != nil {
			log.Fatalln(err)
		}
	}
	
	// indexes
	db.Col("File").CreateHash(false, "Path")
	db.Col("Identifier").CreateFullText(3, "Identifier")
	
	log.Println("Database was recreated")
}
