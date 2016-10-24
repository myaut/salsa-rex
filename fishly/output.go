package fishly

const (
	EOF = iota
	NewLine
	RichText
)

type OutputTagStyle struct {
	// Header (for tabulated output)
	Header string
	
	// Color & style for colorized terminals
	Color string
	Bold bool
	
	// Width of the field for aligned output
	Width int
}

// Maps object.tag to the OutputTagStyle
type OutputTagStyles map[string]OutputTagStyle

type OutputToken struct {
	// For special tokens like EOF, NewLine
	TokenType int
	
	// Text to be put into file
	Text string
	
	// Tag for tagged output (such as table)
	Tag string
}

type IOHandle struct {
	
	objectStack []string
	
	styles OutputTagStyles
}

func (ioh *IOHandle) StartObject(name string, styles OutputTagStyles) {
	
}
