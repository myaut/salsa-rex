package salsarex 

import (
	"fmt"
	"salsacore"
	"encoding/hex"
	"crypto/sha1"
	
	arango "aranGO"
)

// ----------------------------
// REPOSITORY

type Repository struct {
	arango.Document
	
	salsacore.Repository
}

func NewRepository(repo *salsacore.Repository) *Repository {
	obj := new(Repository)
	obj.Repository = *repo
	
	h := sha1.Sum([]byte(fmt.Sprintf("%s:%s", repo.Name, repo.Version)))
	obj.Key = hex.EncodeToString(h[:10])
	
	return obj
}

func (r *Repository) Save() error {
	return db.Col("Repository").Save(r)
}

func (r *Repository) createObjectKey(key string) string {
	h := sha1.Sum([]byte(key))
	return fmt.Sprintf("%s:%s", r.Key, hex.EncodeToString(h[:]))
}

// ----------------------------
// REPOSITORY FILE

type RepositoryFile struct {
	arango.Document
	
	salsacore.RepositoryFile
}

func NewRepositoryFile(repo *Repository, path string) *RepositoryFile {
	obj := new(RepositoryFile)
	
	obj.RepositoryFile.Repository = repo.Key
	obj.RepositoryFile.Path = path 
	obj.RepositoryFile.Tokens = make([]salsacore.Token, 0, 10000)
	
	obj.Key = repo.createObjectKey(path)
	
	return obj
}

func (rf *RepositoryFile) Save() error {
	return db.Col("File").Save(rf)
}

// ----------------------------
// IDENTIFIER

type Identifier struct {
	arango.Document
	
	salsacore.Identifier
}

func NewIdentifier(repo *Repository, identifier string) *Identifier {
	obj := new(Identifier)
	
	obj.Identifier.Identifier = identifier 
	obj.Identifier.Files = make([]string, 0, 20)
	obj.Key = repo.createObjectKey(identifier)
	
	return obj
}

func (i *Identifier) Save() error {
	return db.Col("Identifier").Save(i)
}
