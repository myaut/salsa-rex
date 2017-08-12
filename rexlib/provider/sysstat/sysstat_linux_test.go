package sysstat

import (
	"io/ioutil"
	"os"

	"testing"
)

func TestSFRSimpleStat(t *testing.T) {
	f, err := ioutil.TempFile("", "sysstattest")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.Remove(f.Name())

	f.WriteString("cpu  1 2 3\n")

	var sfr statFileReader

	stat := statistic{sstRaw, "cpu_usr", f.Name(), "cpu", 0, false, true}

	i := sfr.ReadStatistic(&stat)
	if sfr.lastError != nil {
		t.Error(sfr.lastError)
	}
	if i != 1 {
		t.Errorf("Invalid cpu_usr value: 1 expected, got %d", i)
	}
}
