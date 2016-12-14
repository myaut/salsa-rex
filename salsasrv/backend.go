package main

import (
	"salsacore"
	"salsalib"
	
	"net/http"
		
	"github.com/labstack/echo"
)

// "main" function which registers all handlers
func createBackendAPIHandlers(prefix string, server *echo.Echo) {
	server.GET(prefix + "/resetdb", func (ctx echo.Context) error {
		// for debugging
		salsalib.ResetDB()
		ctx.NoContent(http.StatusOK)
		return nil
	})
	
	server.POST(prefix + "/repo", func (ctx echo.Context) error {
		// This is POST request for submitting new repository to parse
		var repo salsacore.Repository
		err := ctx.Bind(&repo);
		if err != nil {
			return err
		}
		
		repoKey, err2 := salsalib.CreateParseTask(&repo)	
		if err2 != nil {
			return err2
		}
			
		ctx.JSON(http.StatusOK, repoKey)
		return nil 
	})
	
	server.GET(prefix + "/repo/:repoKey/taskstatus/:taskType", func (ctx echo.Context) error {
		repoKey := ctx.Param("repoKey")
		taskType := ctx.Param("taskType")
		
		status := salsalib.GetProcessingTaskStatus(repoKey, taskType)
		
		ctx.JSON(http.StatusOK, status)
		return nil
	})
	
	 
}
