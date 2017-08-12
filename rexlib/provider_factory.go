package rexlib

import (
	"rexlib/provider"

	// actual providers
	"rexlib/provider/sysstat"
)

func (incident *Incident) providerFactory(provName string) provider.Provider {
	var provHandle provider.Provider
	switch provName {
	case "sysstat":
		provHandle = sysstat.Create()
	}

	return provHandle
}
