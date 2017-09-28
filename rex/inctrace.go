package main

import (
	"fmt"

	"rexlib"

	"fishly"
	"tsfile"
)

const (
	eventsBatchSize = 32
)

// --------------
// SRV

type IncidentEventArgs struct {
	// Input arguments: name of incident and page tag for series
	Incident string
	Tag      tsfile.TSFPageTag

	// Entries range to retrieve
	Start int
	Count int
}

type IncidentEventReply struct {
	Schema *tsfile.TSFSchemaHeader
	Data   [][]byte
}

func (srv *SRVRex) GetEvents(args *IncidentEventArgs, reply *IncidentEventReply) (err error) {
	incident, err := rexlib.Incidents.Get(args.Incident)
	if err != nil {
		return
	}

	trace, err := incident.GetTraceFile()
	if err != nil {
		return
	}

	reply.Schema, err = trace.GetSchema(args.Tag)
	if err != nil {
		return
	}

	reply.Data = make([][]byte, args.Count)

	return trace.GetEntries(args.Tag, reply.Data, args.Start)
}

// --------------
// CLI

//
// 'get' subcommand -- gets entries
//

type incidentGetCmd struct {
}

type incidentGetOpt struct {
	Series []string `arg:"1"`
}

type incidentGetSeries struct {
	args  IncidentEventArgs
	reply IncidentEventReply

	name    string
	count   uint
	minTime tsfile.TSTimeStart

	deserializer *tsfile.TSFDeserializer
}

func (cmd *incidentGetCmd) NewOptions(cliCtx *fishly.Context) interface{} {
	return new(incidentGetOpt)
}

func (cmd *incidentGetCmd) IsApplicable(cliCtx *fishly.Context) bool {
	ctx := cliCtx.External.(*RexContext)
	return ctx.incident != nil
}

func (cmd *incidentGetCmd) Complete(cliCtx *fishly.Context, rq *fishly.CompleterRequest) {
	ctx := cliCtx.External.(*RexContext)
	if rq.ArgIndex >= 1 {
		if ctx.refreshIncident() != nil {
			return
		}

		// Add all series names as possible arguments
		for _, seriesStats := range ctx.incident.TraceStats.Series {
			rq.AddOptions(seriesStats.Name)
		}
	}
}

func (cmd *incidentGetCmd) Execute(cliCtx *fishly.Context, rq *fishly.Request) (err error) {
	ctx := cliCtx.External.(*RexContext)
	err = ctx.refreshIncident()
	if err != nil {
		return
	}

	opts := rq.Options.(*incidentGetOpt)
	series, err := cmd.newSeriesData(ctx.incident, opts.Series)
	if err != nil {
		return
	}

	// Compute total number of events which we want to process
	var evCount uint
	for _, seriesData := range series {
		evCount += seriesData.count
	}

	// Start output
	ioh, err := rq.StartOutput(cliCtx, false)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()

	ioh.StartObject("series")
	for ; evCount > 0; evCount-- {
		seriesData, err := cmd.getNextSeries(ctx, series)
		if err != nil {
			return err
		}
		if seriesData == nil || len(seriesData.reply.Data) == 0 {
			return fmt.Errorf("%d records are missing", evCount)
		}

		// Extract first entry and reset max time (to be recomputed)
		buf := seriesData.reply.Data[0]
		seriesData.reply.Data = seriesData.reply.Data[1:len(seriesData.reply.Data)]
		seriesData.minTime = 0

		deserializer := seriesData.deserializer

		ioh.StartObject("seriesEntry")
		ioh.WriteString("name", seriesData.name)

		st := deserializer.GetStartTime(buf)
		ioh.WriteFormattedValue("start_time", formatDuration(int64(st)), st)
		et := deserializer.GetEndTime(buf)
		ioh.WriteFormattedValue("end_time", formatDuration(int64(et)), et)

		for fi := 0; fi < deserializer.Len(); fi++ {
			if fi == deserializer.StartTimeIndex || fi == deserializer.EndTimeIndex {
				continue
			}

			name, value := deserializer.Get(buf, fi)
			ioh.WriteRawValue(name, value)
		}
		ioh.EndObject()
	}
	ioh.EndObject()

	return
}

func (cmd *incidentGetCmd) newSeriesData(incident *rexlib.Incident, names []string) (
	series []incidentGetSeries, err error) {
	series = make([]incidentGetSeries, len(names))

	for i, name := range names {
		seriesData := &series[i]
		seriesData.args.Incident = incident.Name
		seriesData.name = name

		for _, seriesStats := range incident.TraceStats.Series {
			if name == seriesStats.Name {
				seriesData.args.Tag = seriesStats.Tag
				seriesData.count = seriesStats.Count
				break
			}
		}

		if seriesData.args.Tag == tsfile.TSFTagEmpty {
			return nil, fmt.Errorf("Series '%s' is not found", name)
		}
	}

	return
}

func (cmd *incidentGetCmd) getNextSeries(ctx *RexContext, series []incidentGetSeries) (
	*incidentGetSeries, error) {

	var mrSeriesIndex int
	var mrSeriesTime tsfile.TSTimeStart

	for seriesIndex, _ := range series {
		seriesData := &series[seriesIndex]

		if len(seriesData.reply.Data) == 0 {
			// No more entries for this data series left
			if uint(seriesData.args.Start) == seriesData.count {
				continue
			}

			// Limit amount of entries retrieved
			seriesData.args.Count = int(seriesData.count) - seriesData.args.Start
			if seriesData.args.Count > eventsBatchSize {
				seriesData.args.Count = eventsBatchSize
			}

			// Retrieve some entries or fail
			var reply IncidentEventReply
			err := ctx.client.Call("SRVRex.GetEvents", &seriesData.args, &reply)
			if err != nil {
				return nil, err
			}

			seriesData.reply.Schema = reply.Schema
			seriesData.reply.Data = append(seriesData.reply.Data, reply.Data...)
			seriesData.args.Start += len(reply.Data)

			if seriesData.deserializer == nil {
				seriesData.deserializer = tsfile.NewDeserializer(reply.Schema)
			}
		}

		// Sort by time: extract it from top-level entry
		if seriesData.minTime == 0 {
			seriesData.minTime = seriesData.deserializer.GetStartTime(
				seriesData.reply.Data[0])
		}

		if mrSeriesTime == 0 || seriesData.minTime < mrSeriesTime {
			mrSeriesTime = seriesData.minTime
			mrSeriesIndex = seriesIndex
		}

	}

	return &series[mrSeriesIndex], nil
}

func formatDuration(t int64) string {
	sign := ""
	if t < 0 {
		t = -t
		sign = "-"
	}

	us2 := t / 100 % 10
	us := t / 1000 % 1000
	ms := t / 1000000 % 1000
	sec := t / 1000000000

	return fmt.Sprintf("%s%ds %03dms %03d.%dus", sign, sec, ms, us, us2)
}
