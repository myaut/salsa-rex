package rexlib

type Provider interface {
	// Prepares collection and finalizes it
	Prepare(handle *IncidentHandle) error
	Finalize(handle *IncidentHandle)

	// Runs a single loop of data collection
	Collect(handle *IncidentHandle)
}
