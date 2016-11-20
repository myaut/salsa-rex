package client

import (
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
