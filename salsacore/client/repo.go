package client

import (
	"salsacore"
)

// Repository that is linked to a server providing it
type ServerRepository struct {
	salsacore.Repository
	
	// Unique key assigned to this repository
	Key string
	
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
	err := hctx.doGETRequestDecodeJSON(repos, path...)
	if err != nil {
		return nil, err
	}
	
	// Assign some internal fields and return value
	for index, _ := range repos {
		repos[index].serverIndex = hctx.serverIndex
	}
	return repos, nil
}
