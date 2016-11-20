package main

import (
	"salsarex"
	"salsacore"
	
	"net/http"
	
	"github.com/labstack/echo"
)

// "main" function which registers all handlers
func createFrontendAPIHandlers(prefix string, server *echo.Echo) {
	findRepostiories := func (ctx echo.Context) (err error) {
		repos, err := salsarex.FindRepositories(&salsacore.Repository {
			Name: ctx.Param("name"),
			Version: ctx.Param("version"),
			Lang: ctx.Param("lang"),
		})
		
		if err != nil {
			return			
		}
		
		ctx.JSON(http.StatusOK, repos)
		return
	}
	server.GET(prefix + "/repo/list", findRepostiories)
	server.GET(prefix + "/repo/list/:name", findRepostiories) 
	server.GET(prefix + "/repo/list/:name/:version", findRepostiories)
	server.GET(prefix + "/repo/list/:name/:version/:lang", findRepostiories)
	
	server.GET(prefix + "/repo/:key", func (ctx echo.Context) (err error) {
		repo, err := salsarex.GetRepository(ctx.Param("key"))
		if repo.Error {
			return echo.NewHTTPError(repo.ErrCode, repo.Message)
		}
		
		ctx.JSON(http.StatusOK, repo)	
		return
	})
}
