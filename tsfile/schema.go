package tsfile

import (
	"fmt"

	"encoding/binary"
	"encoding/json"

	"math"

	"reflect"
)

const (
	TSFFieldBoolean = iota
	TSFFieldInt
	TSFFieldFloat
	TSFFieldString

	TSFFieldStartTime
	TSFFieldEndTime

	TSFFieldEnumerable

	TSFInvalidField
)

type TSFSchemaField struct {
	FieldName [fieldNameLength]byte

	FieldType uint64
	Size      uint64
	Offset    uint64
}

type TSTimeStart int64
type TSTimeEnd int64

type TSBoolean uint32

func FromBoolean(b bool) TSBoolean {
	if b {
		return 1
	}
	return 0
}
func (tsb TSBoolean) ToBoolean() bool {
	return tsb == 1
}

type TSFSchemaHeader struct {
	// Size of entry in bytes
	EntrySize uint16

	// Number of fields in this schema
	FieldCount uint16

	Pad1 uint32
	Pad2 uint64

	Fields [maxFieldCount]TSFSchemaField

	// Name of the schema (only used in V2 header, but doesn't break V1)
	Name [schemaNameLength]byte
}

// Decoded representation of schema
type TSFSchemaFieldInfo struct {
	FieldName string
	FieldType int
	Size      uint
}

type TSFSchemaInfo struct {
	Name      string
	EntrySize uint64
	Fields    []TSFSchemaFieldInfo
}

func NewField(name string, goType reflect.Type) TSFSchemaField {
	var field TSFSchemaField
	copy(field.FieldName[:], []byte(name))
	field.FieldType = TSFInvalidField
	field.Size = uint64(goType.Size())

	switch goType.Kind() {
	case reflect.Uint32:
		switch goType {
		case reflect.TypeOf(TSBoolean(0)):
			field.FieldType = TSFFieldBoolean
		}
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch goType {
		case reflect.TypeOf(TSTimeStart(0)):
			field.FieldType = TSFFieldStartTime
		case reflect.TypeOf(TSTimeEnd(0)):
			field.FieldType = TSFFieldEndTime
		default:
			field.FieldType = TSFFieldInt
		}
	case reflect.Float32, reflect.Float64:
		field.FieldType = TSFFieldFloat
	case reflect.String:
		panic("strings are not properly encoded by encoding/binary")
		field.FieldType = TSFFieldString
	case reflect.Bool:
		panic("booleans are not properly encoded by encoding/binary")
		field.FieldType = TSFFieldBoolean
	case reflect.Array:
		if goType.Elem().Kind() == reflect.Uint8 {
			field.FieldType = TSFFieldString
			field.Size = uint64(goType.Len())
		}
	}

	return field
}

func NewStartTimeField() (field TSFSchemaField) {
	field = TSFSchemaField{
		FieldType: TSFFieldStartTime,
		Size:      8,
	}
	copy(field.FieldName[:], []byte("start_time"))
	return
}

func NewEndTimeField() (field TSFSchemaField) {
	field = TSFSchemaField{
		FieldType: TSFFieldEndTime,
		Size:      8,
	}
	copy(field.FieldName[:], []byte("end_time"))
	return
}

func NewStringField(name string, maxLength int) TSFSchemaField {
	field := NewField(name, reflect.TypeOf(""))
	field.Size = uint64(maxLength)

	return field
}

// Creates new schema header with fields
func NewSchema(name string, fields []TSFSchemaField) (*TSFSchemaHeader, error) {
	schema := new(TSFSchemaHeader)
	copy(schema.Name[:], []byte(name))

	for _, field := range fields {
		fieldId := schema.FieldCount
		if fieldId >= maxFieldCount {
			return nil, fmt.Errorf("Invalid schema: too many fields")
		}
		if field.FieldType == TSFInvalidField {
			return nil, fmt.Errorf("Invalid field type or size %s", DecodeCStr(field.FieldName[:]))
		}

		field.Offset = uint64(schema.EntrySize)

		schema.Fields[fieldId] = field
		schema.EntrySize += uint16(field.Size)
		schema.FieldCount++
	}

	return schema, nil
}

// Create schema based on go structure
func NewStructSchema(goStruct reflect.Type) (*TSFSchemaHeader, error) {
	if goStruct.Kind() != reflect.Struct {
		return nil, fmt.Errorf("Cannot generate schema for non-struct type")
	}

	numFields := goStruct.NumField()
	fields := make([]TSFSchemaField, 0, numFields)

	for i := 0; i < numFields; i++ {
		goField := goStruct.FieldByIndex([]int{i})
		if goField.Anonymous {
			continue
		}

		fields = append(fields, NewField(goField.Name, goField.Type))
	}

	return NewSchema(goStruct.Name(), fields)
}

// Decode schema header info information struct
func (schema *TSFSchemaHeader) Info() (info TSFSchemaInfo) {
	info.Name = DecodeCStr(schema.Name[:])
	info.EntrySize = uint64(schema.EntrySize)

	for fieldId := 0; fieldId < int(schema.FieldCount); fieldId++ {
		field := &schema.Fields[fieldId]
		info.Fields = append(info.Fields, TSFSchemaFieldInfo{
			FieldName: DecodeCStr(field.FieldName[:]),
			FieldType: int(field.FieldType),
			Size:      uint(field.Size),
		})
	}

	return
}

// Checks validity of schema and returns error
func (schema *TSFSchemaHeader) Check(allowExt bool) error {
	if schema.FieldCount > maxFieldCount {
		return fmt.Errorf("Invalid schema: %d fields is too many for TSFile", schema.FieldCount)
	}

	for fieldId := 0; fieldId < int(schema.FieldCount); fieldId++ {
		field := &schema.Fields[fieldId]

		switch field.FieldType {
		case TSFFieldBoolean:
			if field.Size != 4 {
				return fmt.Errorf("Invalid schema field %s: times are not supported without extension",
					DecodeCStr(field.FieldName[:]))
			}

		case TSFFieldString:
			// break;

		case TSFFieldStartTime, TSFFieldEndTime:
			if !allowExt {
				return fmt.Errorf("Invalid schema field %s: times are not supported without extension",
					DecodeCStr(field.FieldName[:]))
			}
			fallthrough
		case TSFFieldInt, TSFFieldEnumerable:
			switch field.Size {
			case 1, 2, 4, 8:
				break
			default:
				return fmt.Errorf("Invalid schema field %s: incorrect size of integer %d",
					DecodeCStr(field.FieldName[:]), field.Size)
			}
		case TSFFieldFloat:
			switch field.Size {
			case uint64(reflect.TypeOf(float32(1)).Size()),
				uint64(reflect.TypeOf(float64(1)).Size()):
				break
			default:
				return fmt.Errorf("Invalid schema field %s: incorrect size of float %d",
					DecodeCStr(field.FieldName[:]), field.Size)
			}
		}
	}

	return nil
}

// Validates that schema matches with other schema or returns error
// if not. Useful for checking versioning of files
func (schema *TSFSchemaHeader) Validate(other *TSFSchemaHeader) error {
	if schema.EntrySize != other.EntrySize ||
		schema.FieldCount != other.FieldCount {
		return fmt.Errorf("Different schema headers: hdr: %d,%d schema: %d,%d",
			schema.EntrySize, schema.FieldCount,
			other.EntrySize, other.FieldCount)

	}

	for fieldId := 0; fieldId < int(schema.FieldCount); fieldId++ {
		field1 := &schema.Fields[fieldId]
		field2 := &other.Fields[fieldId]

		fieldName := DecodeCStr(field1.FieldName[:])
		if fieldName != DecodeCStr(field2.FieldName[:]) {
			return fmt.Errorf("Different schema field %s: other have name %s",
				fieldName, DecodeCStr(field2.FieldName[:]))
		}

		if field1.FieldType != field2.FieldType {
			return fmt.Errorf("Different schema field %s: type mismatch: (%d, %d)",
				fieldName, field1.FieldType, field2.FieldType)
		}

		if field1.Offset != field2.Offset || field1.Size != field2.Size {
			return fmt.Errorf("Different schema field %s: layout mismatch: (%d:%d, %d:%d)",
				fieldName, field1.Offset, field1.Size, field2.Offset, field2.Size)
		}
	}

	return nil
}

func (schema *TSFSchemaHeader) MarshalJSON() ([]byte, error) {
	// Convert to JSON-serializable schema
	type jsonTSFSchemaField struct {
		FieldType string `json:"type"`
		Offset    uint64 `json:"offset"`
		Size      uint64 `json:"size"`
	}
	type jsonTSFSchemaHeader struct {
		Name      string                        `json:"name"`
		EntrySize uint16                        `json:"entry_size"`
		Fields    map[string]jsonTSFSchemaField `json:"fields"`
	}

	jsonSchema := jsonTSFSchemaHeader{
		Name:      DecodeCStr(schema.Name[:]),
		EntrySize: schema.EntrySize,
		Fields:    make(map[string]jsonTSFSchemaField),
	}
	for fieldId := 0; fieldId < int(schema.FieldCount); fieldId++ {
		field := &schema.Fields[fieldId]

		jsonField := jsonTSFSchemaField{
			Offset: field.Offset,
			Size:   field.Size,
		}
		switch field.FieldType {
		case TSFFieldBoolean:
			jsonField.FieldType = "bool"
		case TSFFieldInt:
			jsonField.FieldType = "int"
		case TSFFieldFloat:
			jsonField.FieldType = "float"
		case TSFFieldString:
			jsonField.FieldType = "str"
		case TSFFieldStartTime:
			jsonField.FieldType = "start_time"
		case TSFFieldEndTime:
			jsonField.FieldType = "end_time"
		}

		jsonSchema.Fields[DecodeCStr(field.FieldName[:])] = jsonField
	}

	return json.Marshal(jsonSchema)
}

// Deserialializer works on top of raw buffers ([]byte) and processes raw
// fields from it
type tsFieldDeserializerFunc func(buf []byte) interface{}
type tsFieldDeserializer struct {
	name   string
	offset uint64
	size   uint64

	impl tsFieldDeserializerFunc
}

type TSFDeserializer struct {
	fields []tsFieldDeserializer

	StartTimeIndex int
	EndTimeIndex   int
}

func NewDeserializer(schema *TSFSchemaHeader) *TSFDeserializer {
	deserializer := new(TSFDeserializer)
	deserializer.fields = make([]tsFieldDeserializer, schema.FieldCount)
	deserializer.StartTimeIndex = -1
	deserializer.EndTimeIndex = -1

	for fi, _ := range deserializer.fields {
		field := &deserializer.fields[fi]
		fieldHdr := schema.Fields[fi]

		field.name = DecodeCStr(fieldHdr.FieldName[:])
		field.size = fieldHdr.Size
		field.offset = fieldHdr.Offset

		switch fieldHdr.FieldType {
		case TSFFieldBoolean:
			field.impl = func(buf []byte) interface{} {
				return TSBoolean(binary.LittleEndian.Uint32(buf)).ToBoolean()
			}
		case TSFFieldInt, TSFFieldEnumerable:
			switch fieldHdr.Size {
			case 1:
				field.impl = func(buf []byte) interface{} { return int8(buf[0]) }
			case 2:
				field.impl = func(buf []byte) interface{} {
					return int16(binary.LittleEndian.Uint16(buf))
				}
			case 4:
				field.impl = func(buf []byte) interface{} {
					return int32(binary.LittleEndian.Uint32(buf))
				}
			case 8:
				field.impl = func(buf []byte) interface{} {
					return int64(binary.LittleEndian.Uint64(buf))
				}
			}
		case TSFFieldStartTime:
			deserializer.StartTimeIndex = fi
			field.impl = func(buf []byte) interface{} {
				return TSTimeStart(binary.LittleEndian.Uint64(buf))
			}
		case TSFFieldEndTime:
			deserializer.EndTimeIndex = fi
			field.impl = func(buf []byte) interface{} {
				return TSTimeEnd(binary.LittleEndian.Uint64(buf))
			}
		case TSFFieldFloat:
			switch fieldHdr.Size {
			case 4:
				field.impl = func(buf []byte) interface{} {
					return math.Float32frombits(binary.LittleEndian.Uint32(buf))
				}
			case 8:
				field.impl = func(buf []byte) interface{} {
					return math.Float64frombits(binary.LittleEndian.Uint64(buf))
				}
			}
		case TSFFieldString:
			field.impl = func(buf []byte) interface{} { return DecodeCStr(buf) }
		}
	}

	return deserializer
}

func (deserializer *TSFDeserializer) Len() int {
	return len(deserializer.fields)
}

func (deserializer *TSFDeserializer) Get(buf []byte, idx int) (string, interface{}) {
	field := deserializer.fields[idx]
	buf = buf[field.offset : field.offset+field.size]

	return field.name, field.impl(buf)
}

func (deserializer *TSFDeserializer) GetStartTime(buf []byte) TSTimeStart {
	if deserializer.StartTimeIndex < 0 {
		return TSTimeStart(-1)
	}

	_, value := deserializer.Get(buf, deserializer.StartTimeIndex)
	return value.(TSTimeStart)
}

func (deserializer *TSFDeserializer) GetEndTime(buf []byte) TSTimeEnd {
	if deserializer.EndTimeIndex < 0 {
		return TSTimeEnd(-1)
	}

	_, value := deserializer.Get(buf, deserializer.EndTimeIndex)
	return value.(TSTimeEnd)
}
