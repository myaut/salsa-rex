package tsfile_test

import (
	"tsfile" // PUT

	"testing"
	_ "testing/iotest"

	"io/ioutil"
	"os"

	"reflect"

	"runtime"
)

func runTsfTest(t *testing.T, create func(t *testing.T, f *os.File) *tsfile.TSFile,
	read func(t *testing.T, tsf *tsfile.TSFile)) {
	f, err := ioutil.TempFile("", "tsftest")
	if err != nil {
		t.Error(err)
	}
	defer os.Remove(f.Name())

	// Write tsf file and read from memory
	tsf1 := create(t, f)
	read(t, tsf1)

	storage, err := tsf1.Detach()
	if err != nil {
		t.Error(err)
	}

	t.Log("Re-read file", f.Name())

	// Re-read file and run checks again
	tsf2, err := tsfile.LoadTSFile(storage)
	if err != nil {
		t.Error(err)
	}
	read(t, tsf2)
}

func newFileWithSchema(t *testing.T, f *os.File, v interface{}) (tsf *tsfile.TSFile, tag tsfile.TSFPageTag) {
	tsf, err := tsfile.NewTSFile(f, tsfile.TSFFormatV2)
	if err != nil {
		t.Error(err)
	}

	schema, err := tsfile.NewStructSchema(reflect.TypeOf(v))
	if err != nil {
		t.Error(err)
	}

	tag, err = tsf.AddSchema(schema)
	if err != nil {
		t.Error(err)
	}

	return tsf, tag
}

func TestEmptyFile(t *testing.T) {
	runTsfTest(t, func(t *testing.T, f *os.File) *tsfile.TSFile {
		tsf1, err := tsfile.NewTSFile(f, tsfile.TSFFormatV2)
		if err != nil {
			t.Error(err)
		}
		return tsf1
	}, func(t *testing.T, tsf *tsfile.TSFile) {
		a, b := tsf.GetDataTags()
		if a != b {
			t.Errorf("tsfile has tags [%d;%d)", a, b)
		}
		if tsf.GetEntryCount(a) != -1 {
			t.Errorf("tsfile has entries: %d", tsf.GetEntryCount(a))
		}
	})
}

func TestFileNoEntries(t *testing.T) {
	type S struct {
		I int32
	}
	var tag tsfile.TSFPageTag

	runTsfTest(t, func(t *testing.T, f *os.File) (tsf1 *tsfile.TSFile) {
		tsf1, tag = newFileWithSchema(t, f, S{})

		return tsf1
	}, func(t *testing.T, tsf *tsfile.TSFile) {
		a, b := tsf.GetDataTags()
		if a == b || a != tag {
			t.Errorf("tsfile has tags [%d;%d), %d expected", a, b, tag)
		}
		if tsf.GetEntryCount(tag) != 0 {
			t.Errorf("tsfile has invalid number of entries: %d", tsf.GetEntryCount(tag))
		}
	})
}

func TestFileOneEntry(t *testing.T) {
	type S struct {
		I int32
	}
	var tag tsfile.TSFPageTag

	runTsfTest(t, func(t *testing.T, f *os.File) (tsf1 *tsfile.TSFile) {
		tsf1, tag = newFileWithSchema(t, f, S{})

		entries := make([]interface{}, 1)
		entries[0] = S{5}

		err := tsf1.AddEntries(tag, entries)
		if err != nil {
			t.Error(err)
		}

		return tsf1
	}, func(t *testing.T, tsf *tsfile.TSFile) {
		if tsf.GetEntryCount(tag) != 1 {
			t.Errorf("tsfile has invalid number of entries: %d", tsf.GetEntryCount(tag))
		}

		entries1 := make([]S, 1)
		err := tsf.GetEntries(tag, entries1, 0)
		if err != nil {
			t.Error(err)
		}

		err = tsf.GetEntries(tag, entries1, 1)
		if err == nil {
			t.Error("No entries at index 1, but some are returned")
		}
		t.Log(err)

		rawEntries := make([][]uint8, 1)
		err = tsf.GetEntries(tag, rawEntries, 0)
		if err != nil {
			t.Error(err)
		}
		t.Log(rawEntries)
	})
}

func TestFileTwoSchemas(t *testing.T) {
	type S1 struct {
		I int32
	}
	type S2 struct {
		S [10]byte
	}
	var tag1, tag2 tsfile.TSFPageTag

	runTsfTest(t, func(t *testing.T, f *os.File) (tsf1 *tsfile.TSFile) {
		tsf1, tag1 = newFileWithSchema(t, f, S1{})

		schema2, err := tsfile.NewStructSchema(reflect.TypeOf(S2{}))
		if err != nil {
			t.Error(err)
		}
		tag2, err = tsf1.AddSchema(schema2)
		if err != nil {
			t.Error(err)
		}

		err = tsf1.AddEntries(tag1, []S1{S1{5}})
		if err != nil {
			t.Error(err)
		}
		err = tsf1.AddEntries(tag2, []S1{S1{6}})
		if err == nil {
			t.Errorf("TSFile unexpectedly allows to add entries with invalid tag")
		}
		t.Log(err)

		var s2a, s2b S2
		tsfile.EncodeCStr("a", s2a.S[:])
		tsfile.EncodeCStr("bbbbbbbbbbbbbbbb", s2b.S[:])
		err = tsf1.AddEntries(tag2, []S2{s2a, s2b})
		if err != nil {
			t.Error(err)
		}

		return tsf1
	}, func(t *testing.T, tsf *tsfile.TSFile) {
		if tsf.GetEntryCount(tag1) != 1 {
			t.Errorf("tsfile has invalid number of entries: %d", tsf.GetEntryCount(tag1))
		}

		entries1 := make([]S1, 1)
		err := tsf.GetEntries(tag1, entries1, 0)
		if err != nil {
			t.Error(err)
		}
		if entries1[0].I != 5 {
			t.Errorf("Unexepected value at S1[0]: %d != 5", entries1[0].I)
		}

		// tag2
		if tsf.GetEntryCount(tag2) != 2 {
			t.Errorf("tsfile has invalid number of entries: %d", tsf.GetEntryCount(tag1))
		}

		entries2 := make([]S2, 1)
		err = tsf.GetEntries(tag2, entries2, 0)
		if err != nil {
			t.Error(err)
		}
		if tsfile.DecodeCStr(entries2[0].S[:]) != "a" {
			t.Errorf("Unexpected value at S2[0]: %v != a", entries2[0].S)
		}
		err = tsf.GetEntries(tag2, entries2, 1)
		if err != nil {
			t.Error(err)
		}
		if tsfile.DecodeCStr(entries2[0].S[:]) != "bbbbbbbbb" {
			t.Errorf("Unexpected value at S2[1]: %v != bbbbbbbbb", entries2[0].S)
		}

		stats := tsf.GetStats()
		if len(stats.Series) != 2 {
			t.Errorf("Should return 2 schema stats, got %d", len(stats.Series))
			return
		}
		if stats.Series[0].Count != 1 || stats.Series[0].Name != "S1" {
			t.Errorf("Should get 1 instance of S1 schema stats, got %d of %s",
				stats.Series[0].Count, stats.Series[0].Name)
			return
		}
	})
}

func TestFileAddFile(t *testing.T) {
	type S1 struct {
		I int32
	}
	type S2 struct {
		I int64
	}
	var tag1, tag2 tsfile.TSFPageTag

	runTsfTest(t, func(t *testing.T, f *os.File) (tsf1 *tsfile.TSFile) {
		tsf1, tag1 = newFileWithSchema(t, f, S1{})
		err := tsf1.AddEntries(tag1, []S1{S1{5}})
		if err != nil {
			t.Error(err)
		}

		runTsfTest(t, func(t *testing.T, f *os.File) (tsf2 *tsfile.TSFile) {
			tsf2, tag2 = newFileWithSchema(t, f, S2{})
			err := tsf2.AddEntries(tag2, []S2{S2{50}})
			if err != nil {
				t.Error(err)
			}

			tsf1.AddFile(tsf2)

			return tsf2
		}, func(t *testing.T, tsf2 *tsfile.TSFile) {

		})

		return tsf1
	}, func(t *testing.T, tsf1 *tsfile.TSFile) {
		tag1b, tag2b := tsf1.GetDataTags()
		if tag2b-tag1b != 2 {
			t.Errorf("Invalid tag range [%d:%d]", tag1b, tag2b)
		}
	})
}

var tsfV1Header []byte = []byte{
	// magic                         version
	'T', 'S', 'F', 'I', 'L', 'E', 1, 0,
	// SB[0] time			    count          pad
	1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0,
	// SB[1:3]
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
}
var tsfSchema []byte = []byte{
	// entry_size count  pad
	4, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,

	// field[0].name
	'I', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	// type                    size
	1, 0, 0, 0, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0,
	// offset
	0, 0, 0, 0, 0, 0, 0, 0,
}
var tsfValue []byte = []byte{
	5, 0, 0, 0,
}

func paddedPage(bufs ...[]byte) []byte {
	buf := make([]byte, 0, 4096)
	for _, abuf := range bufs {
		buf = append(buf, abuf...)
	}
	for len(buf) < 4096 {
		buf = append(buf, 0)
	}
	return buf
}

func TestFileRawV1(t *testing.T) {
	type S struct {
		I int32
	}
	runTsfTest(t, func(t *testing.T, f *os.File) *tsfile.TSFile {
		_, err := f.Write(paddedPage(tsfV1Header, tsfSchema))
		if err != nil {
			t.Error(err)
		}
		_, err = f.Write(paddedPage(tsfValue))
		if err != nil {
			t.Error(err)
		}

		tsf, err := tsfile.LoadTSFile(f)
		if err != nil {
			t.Error(err)
		}

		// Get tag and validate loaded schema
		tag, _ := tsf.GetDataTags()

		validSchema, err := tsfile.NewStructSchema(reflect.TypeOf(S{}))
		if err != nil {
			t.Error(err)
		}
		schema, err := tsf.GetSchema(tag)
		if err != nil {
			t.Error(err)
		}
		err = schema.Validate(validSchema)
		if err != nil {
			t.Error(err)
		}

		// Read first entry
		entries := make([]S, 1)
		err = tsf.GetEntries(tag, entries, 0)
		if err != nil {
			t.Error(err)
		}
		if entries[0].I != 5 {
			t.Errorf("Unexpected S[0].I (%d) != 5", entries[0].I)
		}

		// Add one more entry
		err = tsf.AddEntries(tag, []S{S{11}})
		if err != nil {
			t.Error(err)
		}

		return tsf
	}, func(t *testing.T, tsf *tsfile.TSFile) {
		tag, _ := tsf.GetDataTags()
		if tsf.GetEntryCount(tag) != 2 {
			t.Errorf("tsfile has invalid number of entries: %d", tsf.GetEntryCount(tag))
		}

		entries := make([]S, 2)
		err := tsf.GetEntries(tag, entries, 0)
		if err != nil {
			t.Error(err)
		}
		if entries[0].I != 5 {
			t.Errorf("Unexpected S[0].I (%d) != 5", entries[0].I)
		}
		if entries[1].I != 11 {
			t.Errorf("Unexpected S[1].I (%d) != 11", entries[1].I)
		}
	})
}

func TestFileRWParallel(t *testing.T) {
	type S struct {
		L int32
		I int32
	}
	var tag tsfile.TSFPageTag

	N := 10000
	NT := 10

	runTsfTest(t, func(t *testing.T, f *os.File) *tsfile.TSFile {
		tsf, err := tsfile.NewTSFile(f, tsfile.TSFFormatV2)
		if err != nil {
			t.Error(err)
		}

		schema, err := tsfile.NewStructSchema(reflect.TypeOf(S{}))
		if err != nil {
			t.Error(err)
		}
		tag, err = tsf.AddSchema(schema)
		if err != nil {
			t.Error(err)
		}

		numSeq := make(chan int32)
		readerWriter := func() {
			for {
				n := <-numSeq
				if n == 0 {
					return
				}

				// Read last entry (for fun)
				entries := make([]S, 1)
				count := tsf.GetEntryCount(tag)
				if count > 0 {
					tsf.GetEntries(tag, entries, count-1)
				}

				// Write new entry
				entry := &entries[0]
				entry.L = entry.I
				entry.I = n
				tsf.AddEntries(tag, entries)
			}
		}

		// Spawn 10 readers & wait until they write all messages
		for i := 0; i < 10; i++ {
			go readerWriter()
		}
		for i := 1; i <= N; i++ {
			numSeq <- int32(i)
		}
		for tsf.GetEntryCount(tag) != N {
			runtime.Gosched()
		}
		close(numSeq)

		return tsf
	}, func(t *testing.T, tsf *tsfile.TSFile) {
		if tsf.GetEntryCount(tag) != N {
			t.Errorf("tsfile has invalid number of entries: %d", tsf.GetEntryCount(tag))
		}

		entries := make([]S, N)
		err := tsf.GetEntries(tag, entries, 0)
		if err != nil {
			t.Error(err)
		}

		stats := make([]S, N)
		for i, s := range entries {
			// Each entry should be encountered once
			I, L := s.I-1, s.L-1
			if I < 0 || I >= int32(N) {
				t.Errorf("Unexpected I=%d at %d", s.I, i)
			} else {
				stats[I].I++
				if stats[I].I > 1 {
					t.Errorf("Unexpected duplicate of I=%d at %d", s.I, i)
				}
			}

			// We expect that goroutines will make progress and ith goroutine
			// update last entry so they won't stuck at same last entry
			if L < 0 {
				// OK, but at the beginning?
			} else if L >= int32(N) {
				t.Errorf("Unexpected L=%d at %d", s.L, i)
			} else {
				stats[I].L++
				if stats[I].L >= int32(NT) {
					t.Errorf("Unexpected too many of L=%d at %d", s.L, i)
				}
			}
		}
	})
}
