package salsacore

// Indexer-generated types

// -----------------
// IDENTIFIER -- unique identifier in repository

type Identifier struct {
	Repository string
	Identifier string
	
	// list of files those path contain this identifier
	Refs []TokenRef
}
