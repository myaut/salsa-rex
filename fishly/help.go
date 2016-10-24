package fishly 



type helpCmd struct {
	cfg *Config
}
type helpOpt struct {
	Command `arg:"0,opt" help:"If specified, shows full help on this command"`
}

func (*helpCmd) GetDescriptor() CommandDescriptor {
	return CommandDescriptor{
		Name: "help",
		Help: "Shows help on salsa commands",
		Group: "Common",
	}
}
func (*helpCmd) IsApplicable(ctx *Context) bool {
	return true
}
func (*helpCmd) NewOptions() interface{} {
	return new(helpOpt)
}
func (*helpCmd) Complete(ctx *Context, option string) []string {
	// TODO
	return []string{}
}

func (cmd *helpCmd) Execute(ctx *Context, rq *Request) error {
	// Filter out commands by their applicability 
	
	
	return nil
}

