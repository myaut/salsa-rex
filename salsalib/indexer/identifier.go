package indexer

import (
	"salsalib"
	"salsacore"
	
	"sync"
	
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

type IdentifierIndexerFactory struct {
	
}

type IdentifierIndexer struct {
	factory *IdentifierIndexerFactory
	repo *salsalib.Repository
	
	wg sync.WaitGroup
	channel chan identifierIndexerMessage
}

func (factory *IdentifierIndexerFactory) NewIndexer() salsalib.Indexer {
	indexer := new(IdentifierIndexer)
	
	indexer.factory = factory
	indexer.wg.Add(1)
	indexer.channel = make(chan identifierIndexerMessage)
	
	return indexer
}

func (*IdentifierIndexerFactory) InitializeDatabase() error {
	db := salsalib.GetDB()
	
	db.CreateCollection(arango.NewCollectionOptions("Identifier", false))
	
	return db.Col("Identifier").CreateFullText(3, "Identifier")
}
	
func (*IdentifierIndexerFactory) GetName() string {
	return "identifier"
}
func (*IdentifierIndexerFactory) IsApplicable(language string) bool {
	return true
}
func (*IdentifierIndexerFactory) GetDependencies() []string {
	return []string{}
}

func (indexer *IdentifierIndexer) GetFactory() salsalib.IndexerFactory {
	return indexer.factory
}
	
func (indexer *IdentifierIndexer) StartIndexing(repo *salsalib.Repository) error {
	indexer.repo = repo
	go identifierIndexCollector(indexer)
	
	return nil
}
	
func (indexer *IdentifierIndexer) CreateIndex(file *salsalib.RepositoryFile) error {
	for index, token := range file.Tokens {
		if token.Type != salsacore.Ident {
			continue 
		}
		
		indexer.channel <- identifierIndexerMessage {
			repoKey: indexer.repo.Key,
			fileKey: file.Key,
			identifier: token.Text,
			tokenIndex: index,
		}		
	}
	
	return nil
}

func (indexer *IdentifierIndexer) FinishIndexing() {
	// Send "FINISH" message with empty file Key
	indexer.channel <- identifierIndexerMessage {
		repoKey: indexer.repo.Key,
	}
	
	indexer.wg.Wait()
}

func identifierIndexCollector(indexer *IdentifierIndexer) {
	type cachedIdentifier struct {
		Identifier
		salsalib.CachedDocument
	}
	
	identifiers := make(map[string]*cachedIdentifier)
	
	for {
		msg := <- indexer.channel
		
		if msg.repoKey != indexer.repo.Key {
			continue
		}
		if len(msg.fileKey) == 0 {
			break
		}
		
		identifierKey := indexer.repo.GetSubKey(msg.identifier)	
		identifier, ok := identifiers[identifierKey]
		
		// Get identifier from on-stack cache or create new one  
		if !ok {
			identifier = new(cachedIdentifier)
			identifier.Identifier.Key = identifierKey
			identifier.Identifier.Identifier.Identifier = msg.identifier
			identifier.Identifier.Identifier.Refs = make([]salsacore.TokenRef, 0, 20)
			identifier.CachedDocument.Create()
			
			identifiers[identifierKey] = identifier
		} else {
			identifier.CachedDocument.Update()
		}
		
		identifier.Identifier.Refs = append(
			identifier.Identifier.Refs, salsalib.NewTokenRef(msg.tokenIndex, msg.fileKey))
	}
	
	// Now save everything we got here
	saver := salsalib.NewBatchSaver("Identifier")
	defer saver.Complete()
	
	for _, identifier := range identifiers {
		saver.AddDocument(&identifier.CachedDocument, &identifier.Identifier)
	}
	saver.Wait()
	
	indexer.wg.Done()
}

func (*IdentifierIndexerFactory) DeleteIndex(repo *salsalib.Repository) {
	// TODO
}