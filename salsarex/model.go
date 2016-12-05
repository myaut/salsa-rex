package salsarex 

import (
	"fmt"
	
	"os"
	
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
	obj.Key = obj.GetKey()
	
	return obj
}

func NewRepositoryFromKey(repoKey string) *Repository {
	return &Repository{Document: arango.Document{Key: repoKey}}
}

// Generates hash key for a repository with known name, version and 
// language
func (repo *Repository) GetKey() string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s%s%s", repo.Name, 
				repo.Version, repo.Lang)))
	return hex.EncodeToString(h[:10])
}

// Generates hash key for a repository-related object with identifier ident
func (repo *Repository) GetSubKey(ident string) string {
	h := sha1.Sum([]byte(ident))
	return fmt.Sprintf("%s:%s", repo.Key, hex.EncodeToString(h[:]))
}

// ----------------------------
// REPOSITORY FILE

type RepositoryFile struct {
	arango.Document
	
	salsacore.RepositoryFile
}

func NewRepositoryFile(repo *Repository, path string, fi os.FileInfo) *RepositoryFile {
	obj := new(RepositoryFile)
	
	obj.RepositoryFile.Repository = repo.Key
	obj.RepositoryFile.Path = path 
	obj.RepositoryFile.Name = fi.Name()
	
	obj.RepositoryFile.FileType = salsacore.RFTOther
	if fi.IsDir() {
		obj.RepositoryFile.FileType = salsacore.RFTDirectory
	}
	
	obj.RepositoryFile.FileSize = fi.Size()
	
	obj.Key = repo.GetSubKey(path)
	return obj
}

