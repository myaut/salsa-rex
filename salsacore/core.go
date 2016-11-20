package salsacore

// SALSA/REX core -- core types and functions for client and server

import (
	"strings"
	"strconv"
)

// -----------------
// TOKEN

type TokenType int

const (
	// Same as token scanner
	EOF = iota
	Ident
	Keyword
	Int
	Float
	Char
	String
	
	// Other operators and tokens
	Symbol
	
	// #include or import in Java
	PPInclude
	
	// Begin and finish of preprocessor construct (define, if, elif)
	PPBegin
	PPEnd
)

type Token struct {
	Type TokenType
	Text string
	
	// Token position within file
	Line int
	Column int
}


// -----------------
// REPOSITORY & FILE

type Repository struct {
	// Absolute path to sources (should be accessible by server)
	Path string
	
	// Name of repository and corresponding version
	Name string
	Version string
	
	// Language -- one of C, CPP or JAVA
	Lang string

	// List of indexers that were applied to this repository 
	Indexers []string
}

type RepositoryProcessingStatus struct {
	Total int32
	Processed int32
	Indexers int32
}

type RepositoryFile struct {
	Repository string
	Path string
	
	Tokens []Token 
}

type TokenRef struct {
	File string
	Index int
}

// Compares versions and returns negative value if repo < other,
// positive value repo > other or 0 if they are equal
func (repo *Repository) SemverCompare(other Repository) int {
	ver1 := strings.Split(repo.Version, ".")
	ver2 := strings.Split(other.Version, ".")
	
    for i, v1 := range ver1 {
        if i >= len(ver2) {
            // ver1 longer, but all other items are the same
            return 1;
        }
        
        v1i, _ := strconv.Atoi(v1)
        v2i, _ := strconv.Atoi(ver2[i])
        
        delta := v1i - v2i
        if delta != 0 {
            return delta
        }
    }
    
    if len(ver1) < len(ver2) {
        return -1;
    }
    return 0;
}
