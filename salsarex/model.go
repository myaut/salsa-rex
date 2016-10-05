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

func (r *Repository) CreateObjectKey(key string) string {
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
	
	obj.Key = repo.CreateObjectKey(path)
	
	return obj
}

