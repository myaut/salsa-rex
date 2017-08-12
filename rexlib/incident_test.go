package rexlib_test

import (
	"io/ioutil"
	"log"
	"os"

	"testing"

	"rexlib"
	"rexlib/provider"
)

var incidentDir string

func TestStepHelpers(t *testing.T) {
	step0 := &provider.ConfigurationStep{}
	if !step0.EnsureName("x") {
		t.Errorf("Ensure Name '' =~ x")
	}
	if step0.CompareName("x") {
		t.Errorf("Compare Name '' != x")
	}

	step1 := &provider.ConfigurationStep{Name: "x"}
	if !step1.CompareName("x") {
		t.Errorf("Compare Name x == x")
	}
	if !step1.CompareNSName("x", "n") {
		t.Errorf("Compare Name x == n:x")
	}
	if step1.CompareName("y") {
		t.Errorf("Compare Name x != y")
	}

	step2 := &provider.ConfigurationStep{NameSpace: "n", Name: "x"}
	if step2.CompareName("x") {
		t.Errorf("Compare Name n:x != x")
	}
	if !step2.CompareNSName("x", "n") {
		t.Errorf("Compare Name n:x == n:x")
	}

	step3 := &provider.ConfigurationStep{Name: "x", Values: []string{"x", "y"}}
	if !step3.PopValue("x") {
		t.Errorf("Pop Value x")
	}
	if step3.PopValue("x") {
		t.Errorf("Pop Value x twice")
	}
	if step3.PopValue("z") {
		t.Errorf("!Pop Value z")
	}
	if step3.CheckValues() {
		t.Errorf("Check values")
	}
}

func TestIncident(t *testing.T) {
	incident, err := rexlib.Incidents.New(&rexlib.Incident{})
	if err != nil {
		t.Error(err)
		return
	}

	iList, err := rexlib.Incidents.GetList()
	if len(iList) != 1 {
		t.Errorf("%d incidents found", len(iList))
		return
	}
	if iList[0].Name != incident.Name {
		t.Errorf("Incident names '%s' and '%s' do not match (GetList)",
			iList[0].Name, incident.Name)
	}

	other, err := rexlib.Incidents.Get(incident.Name)
	if other.Name != incident.Name {
		t.Errorf("Incident names '%s' and '%s' do not match (Get)",
			other.Name, incident.Name)
	}

	// Now try to configure this little one with sysstat
	pstate := &provider.ConfigurationState{
		ProviderIndex: -1,
		Configuration: []*provider.ConfigurationStep{
			&provider.ConfigurationStep{Values: []string{"sysstat"}},
			&provider.ConfigurationStep{Name: "stat", Values: []string{"cpu_usr"}},
		},
	}
	err = other.ConfigureProvider(provider.ConfigureSetValue, pstate)
	if err != nil {
		t.Error(err)
		return
	}

	if len(pstate.Configuration) != 1 {
		t.Errorf("ConfigureSetValue: %d steps", len(pstate.Configuration))
	}

	pstate.Configuration = []*provider.ConfigurationStep{nil}
	other.ConfigureProvider(provider.ConfigureGetValues, pstate)

	if len(pstate.Configuration) != 1 {
		t.Errorf("ConfigureGetValues: %d step(-s)", len(pstate.Configuration))
	} else if len(pstate.Configuration[0].Values) != 1 {
		t.Errorf("ConfigureGetValues: %d value(-s)", len(pstate.Configuration[0].Values))
	} else if pstate.Configuration[0].Values[0] != "cpu_usr" {
		t.Errorf("ConfigureGetValues: %s != cpu_usr", pstate.Configuration[0].Values[0])
	}
}

func TestMain(m *testing.M) {
	incidentDir, err := ioutil.TempDir("", "rexlib")
	if err != nil {
		log.Fatalln(err)
	}
	defer os.RemoveAll(incidentDir)

	rexlib.Initialize(incidentDir)

	os.Exit(m.Run())
}
