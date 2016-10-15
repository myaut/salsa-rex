package salsarex

import (
	"fmt"
		
	"salsacore"
)


type Indexer interface {
	// Indexes certain file
	StartIndexing(repo *Repository) error
	CreateIndex(file *RepositoryFile) error
	FinishIndexing()
	
	GetFactory() IndexerFactory
}

type IndexerFactory interface {
	// Creates collections and indexes in database
	InitializeDatabase() error
	
	// Returns name (unique) of this indexer
	GetName() string
	// Checks if it is applicable to a repository of certain language
	IsApplicable(language string) bool
	// Returns names of required indexers (which should
	// process source first, before running this one)
	GetDependencies() []string
	
	NewIndexer() Indexer
	
	// Deletes all items corresponding to a repository
	DeleteIndex(repo *Repository)
}


var indexerRegistry = make(map[string]IndexerFactory)

func RegisterIndexer(factory IndexerFactory) {
	indexerRegistry[factory.GetName()] = factory
}

// Sorts list of indexer names and spawns dependent indexes (if they 
// were not yet applied) 
type createIndexersContext struct {
	language string
	indexerInstances []Indexer
	createdIndexers []string
}

// Returns indexer instances for certain repository including its dependencies
func createIndexers(language string, indexers []string, 
					existingIndexers []string) ([]Indexer, error) {
	var context createIndexersContext
	context.language = language
	context.createdIndexers =  existingIndexers[:]
	context.indexerInstances = make([]Indexer, 0, len(indexers))
	
	for _, name := range indexers {
		err := addIndexer(name, &context)
		if err != nil {
			return nil, err
		}
	}
	
	return context.indexerInstances, nil
}

func addIndexer(name string, context *createIndexersContext) error {
	for _, created := range context.createdIndexers {
		if created == name {
			return nil
		}
	}
	
	indexerFactory, ok := indexerRegistry[name]
	if !ok {
		return fmt.Errorf("Requested indexer '%s' is not registered", name)
	}
	if !indexerFactory.IsApplicable(context.language) {
		return fmt.Errorf("Requested indexer '%s' is not applicable", name)
	}
	
	for _, depName := range indexerFactory.GetDependencies() {
		err := addIndexer(depName, context)
		if err != nil {
			return fmt.Errorf("Indexer '%s' depends on indexer '%s' which is not registered", name, depName)
		}
	}
	
	context.createdIndexers = append(context.createdIndexers, name)
	context.indexerInstances = append(context.indexerInstances, indexerFactory.NewIndexer())
	return nil
}

// helper functions

func NewTokenRef(index int, fileKey string) salsacore.TokenRef {
	return salsacore.TokenRef{
		Index: index,
		File: fileKey,
	}
}

