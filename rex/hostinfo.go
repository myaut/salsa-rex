package main

import (
	"fmt"
	"strings"

	"rexlib/hostinfo"

	"fishly"

	"encoding/gob"
)

type SRVHostInfo struct{}

func (srv *SRVHostInfo) initialize() {
	gob.Register(&hostinfo.HIDiskInfo{})
	gob.Register(&hostinfo.HIProcInfo{})
	gob.Register(&hostinfo.HIThreadInfo{})
}

type HIGetNexusArgs struct {
	SubSys  int
	Reprobe bool
	Stats   bool
}

func (srv *SRVHostInfo) HIGetNexus(args *HIGetNexusArgs, reply *hostinfo.HIObject) (err error) {
	nexus, err := hostinfo.GetNexus(args.SubSys, args.Reprobe, args.Stats)
	if nexus != nil {
		*reply = *nexus
	}
	return
}

//
// hostinfo command which provides information local system inventory
//

type hostinfoCmd struct {
	fishly.GlobalCommand
}

type hostinfoCmdOpt struct {
	Tree    bool `opt:"tree,opt"`
	Reprobe bool `opt:"r|reprobe,opt"`
	Stats   bool `opt:"s|stats,opt"`

	SubSys string `arg:"1"`
}

var hostinfoSubsystems []string = []string{
	"proc",
	"disk",
}

func (cmd *hostinfoCmd) NewOptions(ctx *fishly.Context) interface{} {
	return new(hostinfoCmdOpt)
}

func (cmd *hostinfoCmd) Complete(ctx *fishly.Context, rq *fishly.CompleterRequest) {
	switch rq.ArgIndex {
	case 1:
		rq.AddOptions(hostinfoSubsystems...)
	}
}

func (cmd *hostinfoCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	// Match argument #1 to real subsystem name
	opt := rq.Options.(*hostinfoCmdOpt)

	subsysId := -1
	for id, subsysName := range hostinfoSubsystems {
		if subsysName == opt.SubSys {
			subsysId = id
			break
		}
	}
	if subsysId < 0 {
		return fmt.Errorf("Unknown subsystem '%s'", opt.SubSys)
	}

	// Perform external call to rex-t
	var nexus hostinfo.HIObject
	ctx := cliCtx.External.(*RexContext)
	err = ctx.client.Call("SRVHostInfo.HIGetNexus",
		&HIGetNexusArgs{subsysId, opt.Reprobe, opt.Stats}, &nexus)
	if err != nil {
		return
	}

	// Now write objects back
	ioh, err := rq.StartOutput(cliCtx, false)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()

	ioh.StartObject("hiobjects")
	cmd.writeObjectChildren(ioh, &nexus, opt)
	ioh.EndObject()
	return
}

func (cmd *hostinfoCmd) writeObjectChildren(ioh *fishly.IOHandle, nexus *hostinfo.HIObject, opt *hostinfoCmdOpt) {
	for name, obj := range nexus.Children {
		ioh.StartObject("hiobject")

		switch obj.Object.(type) {
		case *hostinfo.HIDiskInfo:
			di := obj.Object.(*hostinfo.HIDiskInfo)
			ioh.StartObject("hidisk")
			ioh.WriteString("name", name)
			ioh.WriteFormattedValue("type", cmd.formatDiskType(di), di.Type)
			// TODO: format as human size
			ioh.WriteRawValue("size", di.Size)
			ioh.WriteString("bus", di.BusType)
			ioh.WriteString("port", di.Port)
			ioh.WriteString("wwn", di.WWN)
			ioh.WriteString("id", di.Identifier)
			ioh.WriteString("model", di.Model)
			ioh.WriteFormattedValue("paths", strings.Join(di.Paths, "\n"), di.Paths)
			ioh.EndObject()
		case *hostinfo.HIProcInfo:
			proc := obj.Object.(*hostinfo.HIProcInfo)
			ioh.StartObject("process")
			ioh.WriteRawValue("uid", proc.UID)
			ioh.WriteRawValue("pid", proc.PID)
			ioh.WriteRawValue("ppid", proc.PPID)
			ioh.WriteRawValue("execname", proc.ExecName)
			ioh.WriteRawValue("comm", proc.CommandLine)
			ioh.EndObject()
		case *hostinfo.HIThreadInfo:
			thread := obj.Object.(*hostinfo.HIThreadInfo)
			ioh.StartObject("thread")
			ioh.WriteRawValue("tid", thread.TID)
			ioh.WriteRawValue("lifetime", thread.Lifetime)
			ioh.WriteRawValue("name", thread.Name)
			if opt.Stats {
				ioh.WriteRawValue("sys_t", thread.STime)
				ioh.WriteRawValue("usr_t", thread.UTime)
				ioh.WriteRawValue("ctxsw", thread.IVCS+thread.VCS)
				ioh.WriteRawValue("pagefault", thread.MinFault+thread.MajFault)
			}
			ioh.EndObject()
		}

		if len(obj.Children) == 0 {
			ioh.EndObject() // hiobject
			continue
		}

		// Print subnodes in tree mode
		if opt.Tree {
			ioh.StartObject("hiobjects")
			cmd.writeObjectChildren(ioh, obj, opt)
			ioh.EndObject()

			ioh.EndObject() // hiobject
		} else {
			// Print names of children
			var children []string
			for child, _ := range obj.Children {
				children = append(children, child)
			}
			ioh.WriteFormattedValue("children", strings.Join(children, ", "), children)

			ioh.EndObject() // hiobject
		}
	}
}

func (cmd *hostinfoCmd) formatDiskType(di *hostinfo.HIDiskInfo) string {
	switch di.Type {
	case hostinfo.HIDTDisk:
		return "disk"
	case hostinfo.HIDTVolume:
		return "vol"
	case hostinfo.HIDTPartition:
		return "part"
	case hostinfo.HIDTPool:
		return "pool"
	}

	return ""
}
