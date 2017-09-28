package tsfile_test

import (
	"tsfile" // PUT

	"testing"

	"bytes"
	"reflect"
	"strings"

	"encoding/binary"
)

func TestGoodSchema(t *testing.T) {
	type S struct {
		I8  int8
		I16 int16
		I32 int32
		I64 int64
		F32 float32
		F64 float64
		S   [1]byte
		S2  [256]byte
		X   tsfile.TSBoolean
	}

	s, err := tsfile.NewStructSchema(reflect.TypeOf(S{}))
	if err != nil {
		t.Error(err)
	}
	if s.FieldCount != 9 {
		t.Errorf("Invalid field count: %d != 9", s.FieldCount)
	}
	if tsfile.DecodeCStr(s.Fields[8].FieldName[:]) != "X" || s.Fields[8].FieldType != tsfile.TSFFieldBoolean {
		t.Errorf("Unexpected last field: '%s', type %d",
			tsfile.DecodeCStr(s.Fields[8].FieldName[:]), s.Fields[8].FieldType)
	}
	err = s.Check(true)
	if err != nil {
		t.Error(err)
	}

	si := s.Info()
	if si.Fields[0].FieldName != "I8" {
		t.Errorf("Unexpected first field in info: '%s'", si.Fields[0].FieldName)
	}
	if len(si.Fields) != 9 {
		t.Errorf("Invalid field count in info: %d != 9", len(si.Fields))
	}
	if si.Name != "S" {
		t.Error("Invalid struct name '%s'", si.Name)
	}
}

func TestLongSchema(t *testing.T) {
	// 65 fields > max (64) field
	type S struct {
		F1, F2, F3, F4, F5      int32
		F6, F7, F8, F9, F10     int32
		F11, F12, F13, F14, F15 int32
		F16, F17, F18, F19, F20 int32
		F21, F22, F23, F24, F25 int32
		F26, F27, F28, F29, F30 int32
		F31, F32, F33, F34, F35 int32
		F36, F37, F38, F39, F40 int32
		F41, F42, F43, F44, F45 int32
		F46, F47, F48, F49, F50 int32
		F51, F52, F53, F54, F55 int32
		F56, F57, F58, F59, F60 int32
		A, B, C, D, E           int32
	}

	s, err := tsfile.NewStructSchema(reflect.TypeOf(S{}))
	if s != nil {
		t.Errorf("Unexpected struct w/ %d fields", s.FieldCount)
	}
	t.Log(err)
	if !strings.Contains(err.Error(), "too many fields") {
		t.Error(err)
	}
}

func TestSchemaChecks(t *testing.T) {
	type S struct {
		I int32
	}

	s1, err := tsfile.NewStructSchema(reflect.TypeOf(S{}))
	if err != nil {
		t.Error(err)
	}
	s2, err := tsfile.NewStructSchema(reflect.TypeOf(S{}))
	if err != nil {
		t.Error(err)
	}

	err = s1.Check(true)
	if err != nil {
		t.Error(err)
	}

	err = s1.Validate(s2)
	if err != nil {
		t.Error(err)
	}

	s1.Fields[0].FieldType = tsfile.TSFFieldFloat

	err = s1.Check(true)
	if err != nil {
		t.Error(err)
	}
	err = s1.Validate(s2)
	t.Log(err)
	if err == nil {
		t.Errorf("Validate shouldn't pass after field 0 type become float")
	}

	s1.Fields[0].Size = 33
	err = s1.Check(true)
	t.Log(err)
	if err == nil {
		t.Errorf("Check shouldn't pass for 33-byte floats")
	}
}

func TestSchemaInterpreter(t *testing.T) {
	type S struct {
		I int32
		T tsfile.TSTimeStart
		B tsfile.TSBoolean
		S [32]byte
	}

	schema, err := tsfile.NewStructSchema(reflect.TypeOf(S{}))
	if err != nil {
		t.Error(err)
		return
	}

	s := S{I: 10, T: tsfile.TSTimeStart(1000), B: tsfile.FromBoolean(true)}
	tsfile.EncodeCStr("abc", s.S[:])

	buf := bytes.NewBuffer([]byte{})
	binary.Write(buf, binary.LittleEndian, &s)

	deser := tsfile.NewDeserializer(schema)

	if _, i := deser.Get(buf.Bytes(), 0); i.(int32) != 10 {
		t.Errorf("I != %v", i)
	}

}
