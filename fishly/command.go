package fishly


type Request struct { 
	Options   interface{}
}

type CommandDescriptor struct {
	// Name of the command
	Name string
	
	// Long help for the command (long lines with |s supported) 
	Help string
	
	// Name of the command group and order to be shown in help
	Group string
}

type Command interface {
	// Returns command descriptor
	GetDescriptor() CommandDescriptor
	
	// Returns true if this command can be used in the current context
	// Note that you may create multiple commands but with varying command
	// contexts (they should be exclusive, though)
	IsApplicable(ctx *Context) bool
	
	// Returns a command-option structure which should be filled in 
	// before creating request to handle options and arguments
	// NOTE: it is used to generate help, so no side effects please
	NewOptions() interface{}
	
	// Returns auto-complete strings for arguments and options
	Complete(ctx *Context, option string) []string
	
	// Execute command 
	Execute(ctx *Context, rq *Request) error
}
