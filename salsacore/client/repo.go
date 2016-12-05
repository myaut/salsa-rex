package client

import (
	"fmt"
	"strings"
	
	"path/filepath"
	fnmatch "path"
	
	"salsacore"
)

// Repository that is linked to a server providing it
type ServerRepository struct {
	salsacore.Repository
	
	// Unique key assigned to this repository
	Key string		`json:"_key"`
	
	// Name of the server providing this repository
	Server string
	
	serverIndex int
}

// Searches over repositories in salsa server using prepared object
// to find them where each object's field is used as search criteria 
// (if it is not empty)
func (hctx *HandleContext) FindRepositories(repo *salsacore.Repository) ([]ServerRepository, error) {
	// Build path in format /repo/list[/:name[/:version[/:lang]]]
	path := []string{"repo", "list"}
	if len(repo.Name) > 0 {
		path = append(path, repo.Name)
		if len(repo.Version) > 0 {
			path = append(path, repo.Version)
			if len(repo.Lang) > 0 {
				path = append(path, repo.Lang)
			}
		}
	}
	
	repos := make([]ServerRepository, 0)
	err := hctx.doGETRequestDecodeJSON(&repos, path...)
	if err != nil {
		return nil, err
	}
	
	// Assign some internal fields and return value
	for index, _ := range repos {
		repos[index].Server = hctx.srv.Name
		repos[index].serverIndex = hctx.serverIndex
	}
	return repos, nil
}

// Tries to get repository by using its key
func (hctx *HandleContext) GetRepository(repoKey string) (ServerRepository, error) {
	var repo ServerRepository
	err := hctx.doGETRequestDecodeJSON(&repo, "repo", repoKey)
	if err == nil {
		repo.Server = hctx.srv.Name
		repo.serverIndex = hctx.serverIndex
	}
	
	return repo, err
}

func (h *Handle) ResetActiveRepository() {
	h.activeServer = -1
	h.repoKey = "" 
}

// Sets repo as active repository in this handle. NewRepositoryContext() 
// will return contexts associated with this repository
func (h *Handle) SelectActiveRepository(repo ServerRepository) {
	h.activeServer = repo.serverIndex
	h.repoKey = repo.Key
}

// Sets repo from server name and key
func (h *Handle) SelectActiveRepositoryEx(serverName, repoKey string) error {
	h.activeServer = -1 
	for serverIndex, srv := range h.Servers {
		if srv.Name == serverName {
			h.activeServer = serverIndex
			break
		}
	}
	if h.activeServer == -1 {
		return fmt.Errorf("Server '%s' is not found", serverName)
	}
	
	h.repoKey = repoKey
	return nil
}

// Returns active repository keys
func (h *Handle) GetActiveRepositoryKeys() (serverName, repoKey string) {
	if h.activeServer == -1 {
		return "", ""
	}
	
	return h.Servers[h.activeServer].Name, h.repoKey
}

// Returns entire file object. Unlike GetDirectoryEntries() doesn't
// allow file masks
func (hctx *HandleContext) GetFileContents(path string) (file salsacore.RepositoryFile, err error) {
	rqPath := []string{"fs", "read/" + path}
	err = hctx.doGETRequestDecodeJSON(&file, rqPath...)
	return
}

// Get directory entries related to path where path can be:
//	- directory -- in this case all entries corresponding to this directory
//		returned
//  - directory with basename being a mask containing '*' or '?' -- 
//		all entries matching to specified mask are returned
//	- file -- only file contents are returned
func (hctx *HandleContext) GetDirectoryEntries(path string) ([]salsacore.RepositoryFile, error) {
	mask := filepath.Base(path)
	if strings.IndexAny(mask, "*?[]") >= 0 {
		path = filepath.Dir(path)
	} else {
		mask = ""
	}
	
	rqPath := []string{"fs", "getdents/" + path}
	
	files := make([]salsacore.RepositoryFile, 0)
	err := hctx.doGETRequestDecodeJSON(&files, rqPath...)
	if err != nil {
		return nil, err
	}
	
	if len(mask) > 0 {
		filteredFiles := make([]salsacore.RepositoryFile, 0, len(files))
		for _, file := range files {
			if ok, _ := fnmatch.Match(mask, file.Name); ok {
				filteredFiles = append(filteredFiles, file)
			}
		}
		
		return filteredFiles, nil
	}
	
	return files, nil 
}
