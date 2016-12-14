package salsalib

import (
	"io"
	"sort"
	"strings"
	
	"salsacore"
	
	"text/scanner"
)

// C/C++/Java lexer tokenizer based on Go text scanner. Since Go
// tokens are similar to C (strings, number literals), we
// re-use it in here with some extensions
//	* end of macro body is detected after pp hash 

// ---------------------
// LEXER

type language struct {
	// sorted list of keywords
	keywords []string
	
	// Trees of supported multi-rune operators compiled from lists of runes
	
	// Operators which consist from two characters. Key is the prefix 
	// list of runes in string is last rune
	twoRuneOperators map[rune]string
	multiRuneOperators map[string]string
}

var languageDefs = createLanguageDefs()
func (lang* language) addKeywords(keywordSlices ...[]string) {
	// Create & sort keywords slice
	for _, keywords := range keywordSlices {
		lang.keywords = append(lang.keywords, keywords...)
	}
	sort.Sort(sort.StringSlice(lang.keywords))
}

func (lang* language) setMultiRuneOps(multiRuneOperatorSlices ...[]string) {
	// Should sum(map(len, multiRuneOperatorSlices)) as len position here,
	// but we are not in Python :(
	lang.twoRuneOperators = make(map[rune]string, 20)
	lang.multiRuneOperators = make(map[string]string, 5)
	
	// Setup multi-rune maps
	for _, multiRuneOperators := range multiRuneOperatorSlices {
		for _, op := range multiRuneOperators {
			n := len(op)
			if n < 2 {
				// Shouldn't be
				continue
			}
			
			// We assume that all ops would be encoded as 1-byte UTF-8
			// so we can treat rune==byte here
			lang.twoRuneOperators[rune(op[0])] += string(op[1])
			if n > 2 {
				lang.multiRuneOperators[op[0:n-2]] += string(op[n-1])
			}
		}
	}
}
func createLanguageDefs() map[string]language { 
	var keywordsALL = []string {
		"break", "case", "char", "const", "continue", "default", "do", 
		"double", "else", "enum", "float", "for", "goto", "if", "int", "long",
		"return", "short", "static", "switch", "void", "volatile", "while", 
	}
	var keywordsCCPP = []string {
		"auto", "extern", "inline", "signed", "sizeof", "struct",
		"typedef", "union", "unsigned", "register",
	}
	var keywordsCPPJAVA = []string {
		"class", "new", "private", "protected", "public", "this", 
		"try", "throw", "catch",
	}
	var keywordsC = []string{
		"_Bool", "_Complex", "offsetof", "restrict", 
	}
	var keywordsCPP = []string {
		"alignas", "alignof", "asm", "constexpr", "decltype", "delete", 
		"explicit", "export", "friend",  "mutable", "namespace", "noexcept", 
		"nullptr", "operator", "static_assert", "template", 
		"thread_local", "typeid", "typename", "union", "using", "virtual",
		// cast system
		"dynamic_cast", "reinterpret_cast", "static_cast", "const_cast",
		// bool & char types
		"bool", "true", "false", "char16_t", "char32_t",  "wchar_t", 	
		// alternative operators
		"and", "and_eq", "xor", "bitand", "bitor", "compl", "xor_eq", 
		"not", "not_eq", "or", "or_eq",
	}
	var keywordsJAVA = []string {
		"abstract", "assert", "package", "synchronized", "boolean", 
		"implements", "protected", "byte", "import", "throws", "instanceof", 
		"transient", "extends",  "final", "interface", "finally",
		"strictfp", "native", "super",
	}
	
	var multiRuneOperatorsALL = []string {
		// Assignment ops
		"+=", "-=", "*=", "/=", "%=", "&=",
		"|=", "^=", "<<=", ">>=",
		// Comparison ops
		"<=", ">=", "!=",
		// Increment & decrement
		"++", "--",
		// Logical operators
		"&&", "||",
		// Shift operators
		">>", ">>",
		// Ellipsis (...) operator
		"...",
	}
	var multiRuneOperatorsCCPP = []string {
		// Arrow operator
		"->",
	}
	
	// Construct languages
	var langC, langCPP, langJAVA language
	langC.addKeywords(keywordsALL, keywordsCCPP, keywordsC)
	langCPP.addKeywords(keywordsALL, keywordsCCPP, keywordsCPPJAVA, keywordsCPP)
	langJAVA.addKeywords(keywordsALL, keywordsCPPJAVA, keywordsJAVA)
	
	langC.setMultiRuneOps(multiRuneOperatorsALL, multiRuneOperatorsCCPP)
	langCPP.setMultiRuneOps(multiRuneOperatorsALL, multiRuneOperatorsCCPP)
	langJAVA.setMultiRuneOps(multiRuneOperatorsALL)
	
	return map[string]language {
		"C": langC,
		"CPP": langCPP,
		"JAVA": langJAVA,
	}
}

// ---------------------
// LEXER

type Lexer struct {
	// Derived from Go text scanner
	scanner.Scanner
	
	// For token fid field (for putting this into DB)
	fileId int
	
	// Line where preprocessor body has started. If set to -1,
	// then no preprocessor directive is currently scanning
	ppLastLine int
	
	// If we injected "generic" runes such as PPEnd, contains
	// last scanned rune from scanner. Otherwise, contains 
	// scanner.EOF 
	lastRune rune
	
	// Number of runes going prior to token text
	initialRunes int
	
	// Language definitions
	language
}

// Initialize lexer with language where language name is one of C, CPP or JAVA
func (l *Lexer) Init(src io.Reader, filename string, languageName string) {
	l.Scanner.Init(src)
	l.Filename = filename
	
	// C doesn't support raw strings
	l.Mode = (scanner.ScanIdents | scanner.ScanFloats | scanner.ScanChars | 
			  scanner.ScanStrings | scanner.ScanComments | scanner.SkipComments)
	
	l.ppLastLine = -1
	l.lastRune = scanner.EOF
	
	l.Error = func(*scanner.Scanner, string) {
		// ignore errors
	}
	
	l.language = languageDefs[languageName]
}

func (l *Lexer) produce(tokenType salsacore.TokenType, token string) salsacore.Token {
	return salsacore.Token{
		Type: tokenType,
		Text: token,
		Line: l.Pos().Line,
		
		// Column points to the first character in token (starting at 1) on contrary
		// to what Scanner returns (first character past token)
		Column: l.Pos().Column - len(token) - l.initialRunes,
	}
}

func (l *Lexer) produceType(tokenType salsacore.TokenType) salsacore.Token {
	return l.produce(tokenType, l.TokenText())
}

func (l *Lexer) produceSymbol(token string) salsacore.Token {
	return l.produce(salsacore.Symbol, token)
}

func (l *Lexer) LexScan() salsacore.Token {
	var r rune
	l.initialRunes = 0
	
	if l.lastRune != scanner.EOF {
		r, l.lastRune = l.lastRune, scanner.EOF
	} else {
		r = l.Scan()
		
		if l.ppLastLine >= 0 && l.Pos().Line > l.ppLastLine {
			// new line started -- preprocessor statement ends here
			l.ppLastLine = -1
			l.lastRune = r
			return l.produce(salsacore.PPEnd, "")
		}
	}
	
	if r == '\\' && l.Peek() == '\n' {
		// Next line of preprocessor statement
		l.ppLastLine = l.Pos().Line + 1;
		r = l.Scan()
	}
	
	switch r {
		case scanner.EOF:
			return l.produceType(salsacore.EOF)
		case scanner.Int:
			return l.produceType(salsacore.Int)
		case scanner.Float:
			return l.produceType(salsacore.Float)
		case scanner.Char:
			return l.produceType(salsacore.Char)
		case scanner.String:
			// FIXME: this breaks u"" strings?
			return l.produceType(salsacore.String)
		case scanner.Ident:
			token := l.TokenText()
			i := sort.SearchStrings(l.keywords, token)
			if i < len(l.keywords) && l.keywords[i] == token {
				return l.produce(salsacore.Keyword, token) 
			} else {
				return l.produce(salsacore.Ident, token)
			}
		case '{', '}', '(', ')', '[', ']', ',', ':', ';':
			return l.produceSymbol(string(r))
		case '#':
			// preprocessor -- actually take next token
			r = l.Scan()
			token := l.TokenText()
			if token == "include" {
				return l.scanPPInclude()
			}
			
			l.ppLastLine = l.Pos().Line
			return l.produce(salsacore.PPBegin, token)
	}

	return l.scanTwoRuneOperator(r)
}

func (l *Lexer) scanTwoRuneOperator(r rune) salsacore.Token {	
	// Try to parse two-rune operator
	runes, isValid := l.twoRuneOperators[r] 
	if isValid && strings.ContainsRune(runes, l.Peek()) {
		r2 := l.Next()
		token := string(r) + string(r2)
		
		// Possible -- three-rune operator
		runes, isValid = l.multiRuneOperators[token]
		if isValid && strings.ContainsRune(runes, l.Peek()) {
			r3 := l.Next()
			return l.produceSymbol(token + string(r3))
		}
		
		return l.produceSymbol(token)
	}
	
	return l.produceSymbol(string(r))
}

func (l *Lexer) scanPPInclude() salsacore.Token {
	r := l.Scan()
	l.initialRunes = 1
				
	if r == scanner.String {
		return l.produce(salsacore.PPInclude, strings.Trim(l.TokenText(), `"`))
	} else if r == '<' {
		path := ""
		for r = l.Scan() ; r != '>' ; r = l.Scan() {
			path += l.TokenText()
		} 
		return l.produce(salsacore.PPInclude, path)
	}
	
	// TODO: Error
	return l.produce(salsacore.EOF, "")
}

