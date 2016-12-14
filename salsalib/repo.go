package salsalib

import (
	"errors"
	
	"strings"
	"path/filepath"
	
	"salsacore"
	
	"aranGO/aql"
)

// Finds list of repositories using repo as a template. All values in 
// repo object are optional 
func FindRepositories(repo *salsacore.Repository) (repos []Repository, err error) {
	// Generate filter for repository, each field is optional
	f := aql.AqlFilter{DefaultKey: "doc"}
	if len(repo.Name) > 0 {
		f.Filters = append(f.Filters, aql.Fil("Name", "==", repo.Name))
		if len(repo.Version) > 0 {
			f.Filters = append(f.Filters, aql.Fil("Version", "==", repo.Version))
			if len(repo.Lang) > 0 {
				f.Filters = append(f.Filters, aql.Fil("Lang", "==", repo.Lang))
			}
		}
		
		// TODO: "hidden" repositories. To avoid long lists when we try to get 
		// all repositories, hide repos with older versions and show them only
		// when they explicitly match name
	}
	
	s := aql.NewAqlStruct().For("doc", "Repository").Filter(f).Return("doc")
	cursor, err := db.Execute(aql.NewQuery(s.Generate()))
	
	if err != nil {
		return 
	}
	if cursor.Err {
		return nil, errors.New(cursor.ErrMsg)
	}
	
	repos = make([]Repository, 0, 1)
	err = cursor.FetchBatch(&repos)
	return 
}

func GetRepository(repoKey string) (repo Repository, err error) {
	err = db.Col("Repository").Get(repoKey, &repo)
	return
}

// Lookups single file node and returns corresponding object 
func GetFileContents(repoKey, path string) (file RepositoryFile, err error) {
	path = strings.TrimRight(path, "/")
	path = filepath.Clean(path)
	
	fileKey := NewRepositoryFromKey(repoKey).GetSubKey(path)
	err = db.Col("File").Get(fileKey, &file)
	return
}

func GetDirectoryEntries(repoKey, path string) (files []RepositoryFile, 
		directory RepositoryFile, err error) {
	if path != "/" {
		directory, err = GetFileContents(repoKey, path)
		if err != nil || directory.Error {
			return
		}
		if directory.FileType != salsacore.RFTDirectory {
			// If not a directory requested, act like GetFileContents()
			return []RepositoryFile{directory}, directory, nil
		}
	} else {
		directory.Path = "/"
	}
	
	// Find all children nodes in this file, but hide their contents
	s := aql.NewAqlStruct().For("file", "File").Filter(
		aql.AqlFilter{
			DefaultKey: "file",
			Filters: []aql.Filter{
				aql.FilBind("Repository", "==", "Repository"),
				aql.FilBind("Parent", "==", "Parent")},
		}).Sort("file.FileType", "DESC", 
				"file.Name", "ASC").Return(`
				   { "Path": file.Path,
					 "Name": file.Name, 
					 "FileType": file.FileType, 
					 "FileSize": file.FileSize } `)
	
	q := aql.NewQuery(s.Generate())
	q.AddBind("Repository", repoKey)
	q.AddBind("Parent", directory.Key)
	
	cursor, err := db.Execute(q)
	if err != nil {
		return 
	}
	
	files = make([]RepositoryFile, 0)
	err = cursor.FetchBatch(&files)
	return
}
