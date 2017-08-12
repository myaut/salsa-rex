package provider

import (
	"errors"
	"log"

	"time"

	"tsfile"
)

type OutputHandle struct {
	Trace *tsfile.TSFile
	Log   *log.Logger

	Now time.Time
}

type ConfigurationAction int

const (
	// Get current configuration as a list of steps
	ConfigureGetValues ConfigurationAction = iota

	// Get configuration hints: options which can be chosen at each configuration
	// step (may be omitted if more generic step wasn't been given values)
	// I.e. when we don't know PID, we can't add TID
	ConfigureGetOptions

	// Set value provided in configuration step. Acts like GetOptions on return
	ConfigureSetValue
)

type ConfigurationStep struct {
	// Namespace of the name of step to allow for dynamically named
	// steps (such as arguments) distinguishable from common steps
	NameSpace string `json:"ns"`
	Name      string `json:"name"`

	Values []string `json:"values"`
}

func (step *ConfigurationStep) CompareStepName(other *ConfigurationStep) bool {
	if step == nil {
		return false
	}
	if len(other.NameSpace) == 0 {
		return step.CompareName(other.Name)
	}
	return step.CompareNSName(other.Name, other.NameSpace)
}

func (step *ConfigurationStep) EnsureName(name string) bool {
	if step == nil {
		return false
	}
	if len(step.Name) == 0 {
		// Anonymous values have same name as default value name
		return true
	}
	return step.CompareName(name)
}

func (step *ConfigurationStep) CompareName(name string) bool {
	if step == nil {
		return false
	}
	if len(step.NameSpace) > 0 {
		return false
	}
	return step.Name == name
}

func (step *ConfigurationStep) CompareNSName(name, ns string) bool {
	if step == nil {
		return false
	}
	if len(step.NameSpace) > 0 && step.NameSpace != ns {
		return false
	}
	return step.Name == name
}

func (step *ConfigurationStep) PopValue(value string) bool {
	if step == nil {
		return false
	}

	newValues := make([]string, len(step.Values))

	off := 0
	for i, item := range step.Values {
		if item == value {
			off++
		} else {
			newValues[i-off] = item
		}
	}
	if off == 0 {
		return false
	}

	step.Values = newValues[:len(step.Values)-off]
	return true
}

func (step *ConfigurationStep) CheckValues() bool {
	if step == nil {
		return true
	}

	// Check for invalid values left by PopValue (they shouldn't be here)
	return len(step.Values) == 0
}

type Provider interface {
	// Tries to configure provider and returns list of available
	// options or error if step name is not valid. Step is only passed for
	// ConfigureSetValues
	Configure(action ConfigurationAction, step *ConfigurationStep) ([]*ConfigurationStep, error)

	// Prepares collection and finalizes it
	Prepare(handle *OutputHandle) error
	Finalize(handle *OutputHandle)

	// Runs a single loop of data collection
	Collect(handle *OutputHandle)
}

type ConfigurationState struct {
	// -1 for new providers, >=0 for existing providers (as returned in
	// IncidentProviderReply)
	ProviderIndex int `json:"index"`
	// Provider options to be added during this step. First options is
	// always a provider name
	Configuration []*ConfigurationStep `json:"config"`
	// When set to 1, commits provider -- mark it as created
	Committed uint32 `json:"committed"`
}

var ErrInvalidConfigurationStep = errors.New("Invalid configuration step name")
var ErrInvalidConfigurationValue = errors.New("Invalid configuration step value")
