package indexer

import (
	"salsarex"
	"salsacore"
	
	arango "aranGO"
)


type Identifier struct {
	arango.Document
	
	salsacore.Identifier
}

// indexer

type identifierIndexerMessage struct {
	repoKey		  string
	fileKey		  string
	identifier    string
	tokenIndex 	  int
}

type IdentifierIndexer struct {
	channel chan identifierIndexerMessage
}

func NewIdentifierIndexer() *IdentifierIndexer {
	indexer := new(IdentifierIndexer)
	indexer.channel = make(chan identifierIndexerMessage)
	
	return indexer
}

func (*IdentifierIndexer) InitializeDatabase() error {
	db := salsarex.GetDB()
	
	db.CreateCollection(arango.NewCollectionOptions("Identifier", false))
	
	return db.Col("Identifier").CreateFullText(3, "Identifier")
}
	
func (*IdentifierIndexer) GetName() string {
	return "identifier"
}
func (*IdentifierIndexer) IsApplicable(language string) bool {
	return true
}
func (*IdentifierIndexer) GetDependencies() []string {
	return []string{}
}
	
func (indexer *IdentifierIndexer) StartIndexing(repo *salsarex.Repository) error {
	go identifierIndexCollector(indexer, repo)
	
	return nil
}
	
func (indexer *IdentifierIndexer) CreateIndex(repo *salsarex.Repository, file *salsarex.RepositoryFile) error {
	for index, token := range file.Tokens {
		if token.Type != salsacore.Ident {
			continue 
		}
		
		indexer.channel <- identifierIndexerMessage {
			repoKey: repo.Key,
			fileKey: file.Key,
			identifier: token.Text,
			tokenIndex: index,
		}		
	}
	
	return nil
}

func (indexer *IdentifierIndexer) FinishIndexing(repo *salsarex.Repository) {
	// Send "FINISH" message with empty file Key
	indexer.channel <- identifierIndexerMessage {
		repoKey: repo.Key,
	}
}

func identifierIndexCollector(indexer *IdentifierIndexer, repo *salsarex.Repository) {
	identifiers := make(map[string]*Identifier)
	
	for {
		msg := <- indexer.channel
		
		if msg.repoKey != repo.Key {
			continue
		}
		if len(msg.fileKey) == 0 {
			break
		}
		
		identifierKey := repo.CreateObjectKey(msg.identifier)	
		identifier, ok := identifiers[identifierKey]
		
		// Get identifier from on-stack cache or create new one  
		if !ok {
			identifier = new(Identifier)
			identifier.Key = identifierKey
			identifier.Identifier.Identifier = msg.identifier
			identifier.Identifier.Refs = make([]salsacore.TokenRef, 0, 20)
			
			identifiers[identifierKey] = identifier
		}
		
		identifier.Identifier.Refs = append(
			identifier.Identifier.Refs, salsarex.NewTokenRef(msg.tokenIndex, msg.fileKey))
		
		// TODO: to support other indexers, we should flush data
		// when file processing is finished...
	}
	
	// Now save objects
	col := salsarex.GetDB().Col("Identifier")
	for _, identifier := range identifiers {
		go col.Save(identifier)
	}
}

func (*IdentifierIndexer) DeleteIndex(repo *salsarex.Repository) {
	// TODO
}