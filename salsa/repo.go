package main

import (
	"fmt"
	"log"

	"time"

	"path/filepath"
	"strings"

	"fishly"

	"salsacore"
	"salsacore/client"
)

var tokenTags = []string{
	"eof",
	"ident",
	"keyword",
	"int",
	"float",
	"char",
	"string",
	"symbol",
	"ppinclude",
	"ppbegin",
	"ppend",
}

//
// ls command for repositories -- shows list of repositories
//

type listReposCmd struct {
}
type listReposOpt struct {
	Server string `opt:"server|s,opt"`

	Name string `arg:"1,opt"`
}

func (*listReposCmd) IsApplicable(ctx *fishly.Context) bool {
	return len(ctx.GetCurrentState().Path) == 0 // only applicable when no repository is picked
}

func (*listReposCmd) NewOptions(ctx *fishly.Context) interface{} {
	return new(listReposOpt)
}

func (cmd *listReposCmd) Complete(ctx *fishly.Context, rq *fishly.CompleterRequest) {
	salsaCtx := ctx.External.(*SalsaContext)

	switch rq.ArgIndex {
	case 0:
		switch rq.Option {
		case "server":
			for _, srv := range salsaCtx.handle.Servers {
				rq.AddOption(srv.Name)
			}
		}
	case 1:
		repos, _ := cmd.findRepositories(salsaCtx, rq.Id, "",
			rq.GetDeadline(), &salsacore.Repository{})
		for _, repo := range repos {
			rq.AddOption(repo.Name)
		}
	}
}

func (cmd *listReposCmd) Execute(ctx *fishly.Context, rq *fishly.Request) (err error) {
	salsaCtx := ctx.External.(*SalsaContext)
	options := rq.Options.(*listReposOpt)

	repos, err := cmd.findRepositories(salsaCtx, rq.Id, options.Server, time.Time{},
		&salsacore.Repository{
			Name: options.Name,
		})
	if err != nil {
		return
	}

	ioh, err := rq.StartOutput(ctx, false)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()

	ioh.StartObject("repositories")
	for _, repo := range repos {
		ioh.StartObject("repository")

		ioh.WriteString("server", repo.Server)
		ioh.WriteString("key", repo.Key)
		ioh.WriteString("name", repo.Name)
		ioh.WriteString("version", repo.Version)
		ioh.WriteString("lang", repo.Lang)

		ioh.EndObject()
	}
	ioh.EndObject()

	return
}

func (*listReposCmd) findRepositories(salsaCtx *SalsaContext, requestId int,
	serverName string, deadline time.Time, repo *salsacore.Repository) ([]client.ServerRepository, error) {
	repos := make([]client.ServerRepository, 0)
	foundServer := false

	for serverIndex, server := range salsaCtx.handle.Servers {
		if len(serverName) > 0 && server.Name != serverName {
			if foundServer {
				break
			}
			continue
		} else {
			foundServer = true
		}

		hctx, err := salsaCtx.handle.NewServerContext(requestId, serverIndex)
		if err != nil {
			return repos, err
		}
		defer hctx.Done()
		hctx.WithDeadline(deadline)

		// Try to use name as repository Key
		srvRepo, err := hctx.GetRepository(repo.Name)
		if err == nil {
			repos = append(repos, srvRepo)
			continue
		}

		srvRepos, err := hctx.FindRepositories(repo)
		if err != nil {
			log.Printf("Error fetching list of repositories from %s: %v", server.Name, err)
			continue
		}

		repos = append(repos, srvRepos...)
	}

	if !foundServer {
		return nil, fmt.Errorf("Not found server '%s'", serverName)
	}
	return repos, nil
}

//
// Select an active repository using select command
//

type selectRepoCmd struct {
	listReposCmd
}
type selectRepoOpt struct {
	Server string `opt:"server|s,opt"`

	Name    string `arg:"1"`
	Version string `arg:"2,opt"`
	Lang    string `arg:"3,opt"`
}

func (*selectRepoCmd) IsApplicable(ctx *fishly.Context) bool {
	return true // always can reselect repo
}
func (*selectRepoCmd) NewOptions(ctx *fishly.Context) interface{} {
	return new(selectRepoOpt)
}
func (cmd *selectRepoCmd) Complete(ctx *fishly.Context, rq *fishly.CompleterRequest) {
	cmd.listReposCmd.Complete(ctx, rq)
}

func (cmd *selectRepoCmd) Execute(ctx *fishly.Context, rq *fishly.Request) error {
	salsaCtx := ctx.External.(*SalsaContext)
	options := rq.Options.(*selectRepoOpt)

	repos, err := cmd.findRepositories(salsaCtx, rq.Id, options.Server, time.Time{},
		&salsacore.Repository{
			Name:    options.Name,
			Version: options.Version,
			Lang:    options.Lang,
		})
	if err != nil {
		return err
	}

	if len(repos) == 0 {
		return fmt.Errorf("Repository '%s' is not found", options.Name)
	}

	// Select repository with most recent version
	repo := repos[0]
	for _, other := range repos {
		if repo.Lang != other.Lang {
			return fmt.Errorf("Ambiguity: multiple repositories with different languages found")
		}
		if repo.SemverCompare(other.Repository) > 0 {
			repo = other
		}
	}

	state := ctx.PushState(true)
	state.Path = []string{repo.Server, repo.Key, repo.Name,
		repo.Version, repo.Lang}
	salsaCtx.handle.SelectActiveRepository(repo)

	return nil
}

//
// ls command for files -- shows list of files
//

type listFilesCmd struct {
}
type listFilesOpt struct {
	Long bool `opt:"l|long,opt"`

	Paths []string `arg:"1,opt"`
}

func (*listFilesCmd) IsApplicable(ctx *fishly.Context) bool {
	return len(ctx.GetCurrentState().Path) >= lengthPathRepo
}

func (*listFilesCmd) NewOptions(ctx *fishly.Context) interface{} {
	return new(listFilesOpt)
}

func (cmd *listFilesCmd) completeImpl(ctx *fishly.Context,
	rq *fishly.CompleterRequest, checker func(fileType salsacore.RepositoryFileType) bool) {
	salsaCtx := ctx.External.(*SalsaContext)

	path := rq.Prefix
	if !strings.HasSuffix(path, "/") {
		path += "*"
	}
	isAbs := strings.HasPrefix(path, "/")
	wd := cmd.getWorkingDirectory(ctx)
	path = cmd.absPath(path, wd)

	hctx, err := salsaCtx.handle.NewRepositoryContext(rq.Id)
	if err != nil {
		return
	}
	hctx.WithDeadline(rq.GetDeadline())

	files, err := hctx.GetDirectoryEntries(path)
	if err != nil {
		return
	}
	for _, file := range files {
		if !checker(file.FileType) {
			continue
		}

		// Depending on which type of arguments we auto-complete (relative vs. absolute)
		// we should keep prefix suggestion or not
		if isAbs {
			rq.AddOption(file.Path)
		} else {
			rq.AddOption(file.Name)
		}
	}
}

func (cmd *listFilesCmd) Complete(ctx *fishly.Context, rq *fishly.CompleterRequest) {
	cmd.completeImpl(ctx, rq, func(fileType salsacore.RepositoryFileType) bool {
		return true
	})
}

func (*listFilesCmd) getWorkingDirectory(ctx *fishly.Context) string {
	return filepath.Join(ctx.GetCurrentState().Path[lengthPathRepo:]...)
}

func (*listFilesCmd) absPath(path string, wd string) string {
	if filepath.HasPrefix(path, "/") {
		return strings.TrimLeft(path, "/")
	}

	return filepath.Join(wd, path)
}

func (cmd *listFilesCmd) Execute(ctx *fishly.Context, rq *fishly.Request) (err error) {
	salsaCtx := ctx.External.(*SalsaContext)
	options := rq.Options.(*listFilesOpt)

	// If no arguments are specified, list contents of current directory.
	// If there are arguments, compute relative paths
	wd := cmd.getWorkingDirectory(ctx)
	if len(options.Paths) == 0 {
		options.Paths = []string{""}
	}
	for index, path := range options.Paths {
		options.Paths[index] = cmd.absPath(path, wd)
	}

	// Get all files from server
	hctx, err := salsaCtx.handle.NewRepositoryContext(rq.Id)
	if err != nil {
		return err
	}

	allFiles := make(map[string][]salsacore.RepositoryFile, 0)
	for _, path := range options.Paths {
		allFiles[path], err = hctx.GetDirectoryEntries(path)
		if err != nil || rq.Cancelled {
			return err
		}
	}

	ioh, err := rq.StartOutput(ctx, false)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()

	ioh.StartObject("fileLists")
	for rootPath, files := range allFiles {
		ioh.StartObject("fileList")

		if len(rootPath) != 0 {
			ioh.WriteFormattedValue("path", fmt.Sprintf("%s contents:", rootPath), rootPath)
		}

		ioh.StartObject("files")
		for _, file := range files {
			typeName, fileName := "file", file.Name
			switch file.FileType {
			case salsacore.RFTDirectory:
				typeName, fileName = "dir", file.Name+"/"
			case salsacore.RFTText:
				typeName = "text"
			case salsacore.RFTSource:
				typeName = "source"
			}

			if options.Long {
				ioh.StartObject("entry")
				ioh.WriteString("type", typeName)
				ioh.WriteRawValue("size", file.FileSize)
				ioh.WriteString("name", file.Name)
				ioh.EndObject()
			} else {
				ioh.WriteString(typeName, fileName)
			}
		}
		ioh.EndObject()
		ioh.EndObject()
	}
	ioh.EndObject()

	return
}

//
// 'cd' command in repository virtual file system. Overrides "cd" builtin
//

type changePathCmd struct {
	listFilesCmd
}
type changePathOpt struct {
	Path string `arg:"1"`
}

func (*changePathCmd) IsApplicable(ctx *fishly.Context) bool {
	return len(ctx.GetCurrentState().Path) >= lengthPathRepo
}

func (*changePathCmd) NewOptions(ctx *fishly.Context) interface{} {
	return new(changePathOpt)
}

func (cmd *changePathCmd) Complete(ctx *fishly.Context, rq *fishly.CompleterRequest) {
	cmd.listFilesCmd.completeImpl(ctx, rq, func(fileType salsacore.RepositoryFileType) bool {
		return fileType == salsacore.RFTDirectory
	})
}

func (cmd *changePathCmd) Execute(ctx *fishly.Context, rq *fishly.Request) (err error) {
	salsaCtx := ctx.External.(*SalsaContext)
	options := rq.Options.(*changePathOpt)

	path := cmd.listFilesCmd.absPath(options.Path,
		cmd.listFilesCmd.getWorkingDirectory(ctx))

	// Special case for paths that lead to root
	if path == "." {
		state := ctx.PushState(false)
		state.Path = state.Path[:lengthPathRepo]

		return
	}

	// In other cases -- find corresponding fs node
	hctx, err := salsaCtx.handle.NewRepositoryContext(rq.Id)
	if err != nil {
		return err
	}
	dir, err := hctx.GetFileContents(path)
	if err != nil || rq.Cancelled {
		return err
	}
	if dir.FileType != salsacore.RFTDirectory {
		return fmt.Errorf("'%s' is not a directory", dir.Path)
	}

	// Update context state
	state := ctx.PushState(false)
	state.Path = append(state.Path[:lengthPathRepo],
		strings.Split(strings.TrimLeft(dir.Path, fishly.PathSeparator),
			fishly.PathSeparator)...)

	return
}

//
// 'cat' command for files -- prints file contents
//

type printFileCmd struct {
	listFilesCmd
}
type printFileOpt struct {
	LineNumbers bool `opt:"n|numbers,opt"`

	Path string `arg:"1"`
}

func (*printFileCmd) IsApplicable(ctx *fishly.Context) bool {
	return len(ctx.GetCurrentState().Path) >= lengthPathRepo
}

func (*printFileCmd) NewOptions(ctx *fishly.Context) interface{} {
	return new(printFileOpt)
}

func (cmd *printFileCmd) Complete(ctx *fishly.Context, rq *fishly.CompleterRequest) {
	cmd.listFilesCmd.completeImpl(ctx, rq, func(fileType salsacore.RepositoryFileType) bool {
		return fileType != salsacore.RFTDirectory
	})
}

func (cmd *printFileCmd) Execute(ctx *fishly.Context, rq *fishly.Request) (err error) {
	salsaCtx := ctx.External.(*SalsaContext)
	options := rq.Options.(*printFileOpt)

	path := cmd.listFilesCmd.absPath(options.Path,
		cmd.listFilesCmd.getWorkingDirectory(ctx))

	hctx, err := salsaCtx.handle.NewRepositoryContext(rq.Id)
	if err != nil {
		return err
	}
	text, err := hctx.GetFileContents(path)
	if err != nil || rq.Cancelled {
		return err
	}
	if text.FileType != salsacore.RFTText && text.FileType != salsacore.RFTSource {
		return fmt.Errorf("'%s' is not a text file", text.Path)
	}

	ioh, err := rq.StartOutput(ctx, true)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()

	// Since tokens and lines array ordered by line number, we iterate
	// them simultaneously.
	tokenIndex := 0

	ioh.StartObject("lines")
	for lineno, line := range text.Lines {
		// Use line numbering from 1 (token columns also do that)
		lineno++
		ioh.StartObject("line")
		if options.LineNumbers {
			ioh.WriteRawValue("lineno", lineno)
		}

		// Print line, but print tokens separately, with tags
		if tokenIndex >= len(text.Tokens) || text.Tokens[tokenIndex].Line > lineno {
			ioh.WriteText(line)
		} else {
			column := 0
			for column < len(line) && tokenIndex < len(text.Tokens) {
				token := text.Tokens[tokenIndex]
				if token.Line > lineno {
					break
				}

				tokenColumn := token.Column - 1
				if tokenColumn >= len(line) {
					// Impossible condition -- lexer error, rewind until last token
					ioh.WriteString("error", fmt.Sprintf("token outside line: %d >= %d",
						token.Column, len(line)))
					tokenIndex = len(text.Tokens)
					break
				}
				if tokenColumn > column {
					ioh.WriteText(line[column:tokenColumn])
				}

				// Write token (but we want to support syntax highlighting here)
				ioh.WriteString(tokenTags[token.Type], token.Text)
				column = tokenColumn + len(token.Text)
				// ioh.WriteRawValue("_col", column)
				tokenIndex++
			}

			// Write tail of line (if there is something to write)
			if len(line) > column {
				ioh.WriteText(line[column:])
			}
		}

		ioh.EndObject()
	}
	ioh.EndObject()

	return
}
