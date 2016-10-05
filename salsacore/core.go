package salsacore

// SALSA/REX core -- core types and functions for client and server

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
