package tsfile

import (
	"fmt"
	
	"reflect"
	"encoding/json"
)

const (
	TSFBoolean = iota
	TSFInt
	TSFFloat
	TSFString
	
	TSFInvalidField
)

type TSFSchemaField struct {
	FieldName [fieldNameLength]byte
	
	FieldType uint64
	Size uint64
	Offset uint64
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
	Size uint
}

type TSFSchemaInfo struct {
	Name string
	EntrySize uint64
	Fields []TSFSchemaFieldInfo
}

func NewField(name string, goType reflect.Type) TSFSchemaField {
	var field TSFSchemaField
	copy(field.FieldName[:], []byte(name))
	field.FieldType = TSFInvalidField
	field.Size = uint64(goType.Size())
	
	switch goType.Kind() {
		case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			field.FieldType = TSFInt
		case reflect.Float32, reflect.Float64:
			field.FieldType = TSFFloat
		case reflect.String:
			field.FieldType = TSFString
		case reflect.Bool:
			field.FieldType = TSFBoolean
		case reflect.Array:
			if goType.Elem().Kind() == reflect.Uint8 {
				field.FieldType = TSFString
				field.Size = uint64(goType.Len())
			}
	}
	
	return field
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
			return nil, fmt.Errorf("Invalid field type or size %s", string(field.FieldName[:]))
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
	
	for i := 0; i < numFields ; i++ {
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
	
	for fieldId := 0 ; fieldId < int(schema.FieldCount) ; fieldId++ {
		field := &schema.Fields[fieldId]
		info.Fields = append(info.Fields, TSFSchemaFieldInfo{
				FieldName: DecodeCStr(field.FieldName[:]),
				FieldType: int(field.FieldType),
				Size: uint(field.Size),
		})
	}
	
	return
}

// Checks validity of schema and returns error 
func (schema *TSFSchemaHeader) Check() error {
	if schema.FieldCount > maxFieldCount {
		return fmt.Errorf("Invalid schema: %d fields is too many for TSFile", schema.FieldCount)
	}
	
	for fieldId := 0 ; fieldId < int(schema.FieldCount) ; fieldId++ {
		field := &schema.Fields[fieldId]
		
		switch field.FieldType {
			case TSFBoolean, TSFString:
				// break;
			
			case TSFInt:
				switch field.Size {
					case 1, 2, 4, 8:
						break
					default:
						return fmt.Errorf("Invalid schema field %s: incorrect size of integer %d",
							 DecodeCStr(field.FieldName[:]), field.Size)
				}
			case TSFFloat:
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
	if (schema.EntrySize != other.EntrySize ||
		schema.FieldCount != other.FieldCount)  {
		return fmt.Errorf("Different schema headers: hdr: %d,%d schema: %d,%d", 
			schema.EntrySize, schema.FieldCount,
			other.EntrySize, other.FieldCount)
			
	}
	
	for fieldId := 0 ; fieldId < int(schema.FieldCount) ; fieldId++ {
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
		
		if (field1.Offset != field2.Offset || field1.Size != field2.Size) {
			return fmt.Errorf("Different schema field %s: layout mismatch: (%d:%d, %d:%d)",
				fieldName, field1.Offset, field1.Size, field2.Offset, field2.Size)
		}
	}
	
	return nil
}

func (schema *TSFSchemaHeader) MarshalJSON() ([]byte, error) {
	// Convert to JSON-serializable schema
	type jsonTSFSchemaField struct{
		FieldType string		`json:"type"`
		Offset uint64			`json:"offset"`
		Size uint64				`json:"size"`
	}
	type jsonTSFSchemaHeader struct{
		Name string			    `json:"name"`
		EntrySize uint16		`json:"entry_size"`
		Fields map[string]jsonTSFSchemaField `json:"fields"`
	}
	
	jsonSchema := jsonTSFSchemaHeader{
		Name: DecodeCStr(schema.Name[:]),
		EntrySize: schema.EntrySize,
		Fields: make(map[string]jsonTSFSchemaField),
	}
	for fieldId := 0 ; fieldId < int(schema.FieldCount) ; fieldId++ {
		field := &schema.Fields[fieldId]
		
		jsonField := jsonTSFSchemaField{
			Offset: field.Offset,
			Size: field.Size,
		}
		switch field.FieldType {
			case TSFBoolean:
				jsonField.FieldType = "bool"
			case TSFInt:
				jsonField.FieldType = "int"
			case TSFFloat:
				jsonField.FieldType = "float"
			case TSFString:
				jsonField.FieldType = "str"	
		}
		
		jsonSchema.Fields[DecodeCStr(field.FieldName[:])] = jsonField
	}
	
	return json.Marshal(jsonSchema)
}
