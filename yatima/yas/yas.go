package main

import (
	"fmt"
	"os"

	"flag"

	"yatima"
)

var usage string = `
Yatima Assembler (YAS)

Usage: yas subcommand [options] [args...]
Subcommands:
	as -o binary.yab source1.yas...
	dump [-m] binary.yab
`

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	var err error

	switch os.Args[1] {
	case "as":
		compile := flag.NewFlagSet("as", flag.ExitOnError)
		outFile := compile.String("o", "", "output yab file")

		compile.Parse(os.Args[2:])

		if compile.NArg() == 0 {
			err = fmt.Errorf("At least one input source required")
			break
		}

		err = assemble(*outFile, compile.Args())
	case "dump":
		dump := flag.NewFlagSet("dump", flag.ExitOnError)
		showModel := dump.Bool("m", false, "show model")
		dump.Parse(os.Args[2:])

		if dump.NArg() != 1 {
			err = fmt.Errorf("Only one input is expected")
			break
		}

		err = dumpFile(dump.Args()[0], *showModel)
	case "-h":
		fmt.Println(usage)
	default:
		err = fmt.Errorf("Please provide valid subcommand")
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func assemble(outFile string, inFiles []string) (err error) {
	outf, err := os.Create(outFile)
	if err != nil {
		return
	}
	defer outf.Close()

	yabw, err := yatima.NewWriter(outf)
	if err != nil {
		return
	}

	for _, inFile := range inFiles {
		err = assembleFile(yabw, inFile)
		if err != nil {
			yabw.Close()
			return
		}
	}

	return yabw.Close()
}

func assembleFile(yabw *yatima.BinaryWriter, inFile string) (err error) {
	inf, err := os.Open(inFile)
	if err != nil {
		return
	}
	defer inf.Close()

	programs, compileErr := yatima.Compile(inf)
	if compileErr != nil {
		return fmt.Errorf("%s:%d %v", inFile, compileErr.LineNo, compileErr.Text)
	}

	for _, prog := range programs {
		err = yabw.AddProgram(prog)
		if err != nil {
			return
		}
	}

	return nil
}
