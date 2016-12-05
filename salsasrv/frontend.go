package main

import (
	"log"
	
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
	
	server.GET(prefix + "/repo/:key/fs/read*", func (ctx echo.Context) (err error) {
		path := "/" + ctx.P(1)
		file, err := salsarex.GetFileContents(ctx.Param("key"), path)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		} 
		if file.Error {
			return echo.NewHTTPError(file.ErrCode, file.Message)
		}
		
		ctx.JSON(http.StatusOK, file)	
		return
	})
	
	server.GET(prefix + "/repo/:key/fs/getdents*", func (ctx echo.Context) (err error) {
		path := "/" + ctx.P(1)
		entries, dir, err := salsarex.GetDirectoryEntries(ctx.Param("key"), path)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		} 
		if dir.Error {
			return echo.NewHTTPError(dir.ErrCode, dir.Message)
		}
		
		ctx.JSON(http.StatusOK, entries)
		return
	})
	
	log.Printf("Created front-end handlers %s", prefix)
}
