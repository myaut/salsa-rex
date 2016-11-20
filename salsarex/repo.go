package salsarex

import (
	"errors"
	
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
