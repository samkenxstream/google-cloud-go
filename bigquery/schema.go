// Copyright 2015 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bigquery

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"

	bq "google.golang.org/api/bigquery/v2"
)

// Schema describes the fields in a table or query result.
type Schema []*FieldSchema

// Relax returns a version of the schema where no fields are marked
// as Required.
func (s Schema) Relax() Schema {
	var out Schema
	for _, v := range s {
		relaxed := &FieldSchema{
			Name:        v.Name,
			Description: v.Description,
			Repeated:    v.Repeated,
			Required:    false,
			Type:        v.Type,
			Schema:      v.Schema.Relax(),
		}
		out = append(out, relaxed)
	}
	return out
}

// ToJSONFields exposes the schema as a JSON array of
// TableFieldSchema objects: https://cloud.google.com/bigquery/docs/reference/rest/v2/tables#TableFieldSchema
//
// Generally this isn't needed for direct usage of this library, but is
// provided for use cases where you're interacting with other tools
// that consume the underlying API representation directly such as the
// BQ CLI tool.
func (s Schema) ToJSONFields() ([]byte, error) {
	var rawSchema []*bq.TableFieldSchema
	for _, f := range s {
		rawSchema = append(rawSchema, f.toBQ())
	}
	// Use json.MarshalIndent to make the output more human-readable.
	return json.MarshalIndent(rawSchema, "", " ")
}

// FieldSchema describes a single field.
type FieldSchema struct {
	// The field name.
	// Must contain only letters (a-z, A-Z), numbers (0-9), or underscores (_),
	// and must start with a letter or underscore.
	// The maximum length is 128 characters.
	Name string

	// A description of the field. The maximum length is 16,384 characters.
	Description string

	// Whether the field may contain multiple values.
	Repeated bool
	// Whether the field is required.  Ignored if Repeated is true.
	Required bool

	// The field data type.  If Type is Record, then this field contains a nested schema,
	// which is described by Schema.
	Type FieldType

	// Annotations for enforcing column-level security constraints.
	PolicyTags *PolicyTagList

	// Describes the nested schema if Type is set to Record.
	Schema Schema

	// Maximum length of the field for STRING or BYTES type.
	//
	// It is invalid to set value for types other than STRING or BYTES.
	//
	// For STRING type, this represents the maximum UTF-8 length of strings
	// allowed in the field. For BYTES type, this represents the maximum
	// number of bytes in the field.
	MaxLength int64

	// Precision can be used to constrain the maximum number of
	// total digits allowed for NUMERIC or BIGNUMERIC types.
	//
	// It is invalid to set values for Precision for types other than
	// NUMERIC or BIGNUMERIC.
	//
	// For NUMERIC type, acceptable values for Precision must
	// be: 1 ≤ (Precision - Scale) ≤ 29. Values for Scale
	// must be: 0 ≤ Scale ≤ 9.
	//
	// For BIGNUMERIC type, acceptable values for Precision must
	// be: 1 ≤ (Precision - Scale) ≤ 38. Values for Scale
	// must be: 0 ≤ Scale ≤ 38.
	Precision int64

	// Scale can be used to constrain the maximum number of digits
	// in the fractional part of a NUMERIC or BIGNUMERIC type.
	//
	// If the Scale value is set, the Precision value must be set as well.
	//
	// It is invalid to set values for Scale for types other than
	// NUMERIC or BIGNUMERIC.
	//
	// See the Precision field for additional guidance about valid values.
	Scale int64
}

func (fs *FieldSchema) toBQ() *bq.TableFieldSchema {
	tfs := &bq.TableFieldSchema{
		Description: fs.Description,
		Name:        fs.Name,
		Type:        string(fs.Type),
		PolicyTags:  fs.PolicyTags.toBQ(),
		MaxLength:   fs.MaxLength,
		Precision:   fs.Precision,
		Scale:       fs.Scale,
	}

	if fs.Repeated {
		tfs.Mode = "REPEATED"
	} else if fs.Required {
		tfs.Mode = "REQUIRED"
	} // else leave as default, which is interpreted as NULLABLE.

	for _, f := range fs.Schema {
		tfs.Fields = append(tfs.Fields, f.toBQ())
	}

	return tfs
}

// PolicyTagList represents the annotations on a schema column for enforcing column-level security.
// For more information, see https://cloud.google.com/bigquery/docs/column-level-security-intro
type PolicyTagList struct {
	Names []string
}

func (ptl *PolicyTagList) toBQ() *bq.TableFieldSchemaPolicyTags {
	if ptl == nil {
		return nil
	}
	return &bq.TableFieldSchemaPolicyTags{
		Names: ptl.Names,
	}
}

func bqToPolicyTagList(pt *bq.TableFieldSchemaPolicyTags) *PolicyTagList {
	if pt == nil {
		return nil
	}
	return &PolicyTagList{
		Names: pt.Names,
	}
}

func (s Schema) toBQ() *bq.TableSchema {
	var fields []*bq.TableFieldSchema
	for _, f := range s {
		fields = append(fields, f.toBQ())
	}
	return &bq.TableSchema{Fields: fields}
}

func bqToFieldSchema(tfs *bq.TableFieldSchema) *FieldSchema {
	fs := &FieldSchema{
		Description: tfs.Description,
		Name:        tfs.Name,
		Repeated:    tfs.Mode == "REPEATED",
		Required:    tfs.Mode == "REQUIRED",
		Type:        FieldType(tfs.Type),
		PolicyTags:  bqToPolicyTagList(tfs.PolicyTags),
		MaxLength:   tfs.MaxLength,
		Precision:   tfs.Precision,
		Scale:       tfs.Scale,
	}

	for _, f := range tfs.Fields {
		fs.Schema = append(fs.Schema, bqToFieldSchema(f))
	}
	return fs
}

func bqToSchema(ts *bq.TableSchema) Schema {
	if ts == nil {
		return nil
	}
	var s Schema
	for _, f := range ts.Fields {
		s = append(s, bqToFieldSchema(f))
	}
	return s
}

// FieldType is the type of field.
type FieldType string

const (
	// StringFieldType is a string field type.
	StringFieldType FieldType = "STRING"
	// BytesFieldType is a bytes field type.
	BytesFieldType FieldType = "BYTES"
	// IntegerFieldType is a integer field type.
	IntegerFieldType FieldType = "INTEGER"
	// FloatFieldType is a float field type.
	FloatFieldType FieldType = "FLOAT"
	// BooleanFieldType is a boolean field type.
	BooleanFieldType FieldType = "BOOLEAN"
	// TimestampFieldType is a timestamp field type.
	TimestampFieldType FieldType = "TIMESTAMP"
	// RecordFieldType is a record field type. It is typically used to create columns with repeated or nested data.
	RecordFieldType FieldType = "RECORD"
	// DateFieldType is a date field type.
	DateFieldType FieldType = "DATE"
	// TimeFieldType is a time field type.
	TimeFieldType FieldType = "TIME"
	// DateTimeFieldType is a datetime field type.
	DateTimeFieldType FieldType = "DATETIME"
	// NumericFieldType is a numeric field type. Numeric types include integer types, floating point types and the
	// NUMERIC data type.
	NumericFieldType FieldType = "NUMERIC"
	// GeographyFieldType is a string field type.  Geography types represent a set of points
	// on the Earth's surface, represented in Well Known Text (WKT) format.
	GeographyFieldType FieldType = "GEOGRAPHY"
	// BigNumericFieldType is a numeric field type that supports values of larger precision
	// and scale than the NumericFieldType.
	BigNumericFieldType FieldType = "BIGNUMERIC"
	// IntervalFieldType is a representation of a duration or an amount of time.
	IntervalFieldType FieldType = "INTERVAL"
)

var (
	errEmptyJSONSchema = errors.New("bigquery: empty JSON schema")
	fieldTypes         = map[FieldType]bool{
		StringFieldType:     true,
		BytesFieldType:      true,
		IntegerFieldType:    true,
		FloatFieldType:      true,
		BooleanFieldType:    true,
		TimestampFieldType:  true,
		RecordFieldType:     true,
		DateFieldType:       true,
		TimeFieldType:       true,
		DateTimeFieldType:   true,
		NumericFieldType:    true,
		GeographyFieldType:  true,
		BigNumericFieldType: true,
		IntervalFieldType:   true,
	}
	// The API will accept alias names for the types based on the Standard SQL type names.
	fieldAliases = map[FieldType]FieldType{
		"BOOL":       BooleanFieldType,
		"FLOAT64":    FloatFieldType,
		"INT64":      IntegerFieldType,
		"STRUCT":     RecordFieldType,
		"DECIMAL":    NumericFieldType,
		"BIGDECIMAL": BigNumericFieldType,
	}
)

var typeOfByteSlice = reflect.TypeOf([]byte{})

// InferSchema tries to derive a BigQuery schema from the supplied struct value.
// Each exported struct field is mapped to a field in the schema.
//
// The following BigQuery types are inferred from the corresponding Go types.
// (This is the same mapping as that used for RowIterator.Next.) Fields inferred
// from these types are marked required (non-nullable).
//
//   STRING      string
//   BOOL        bool
//   INTEGER     int, int8, int16, int32, int64, uint8, uint16, uint32
//   FLOAT       float32, float64
//   BYTES       []byte
//   TIMESTAMP   time.Time
//   DATE        civil.Date
//   TIME        civil.Time
//   DATETIME    civil.DateTime
//   NUMERIC     *big.Rat
//
// The big.Rat type supports numbers of arbitrary size and precision. Values
// will be rounded to 9 digits after the decimal point before being transmitted
// to BigQuery. See https://cloud.google.com/bigquery/docs/reference/standard-sql/data-types#numeric-type
// for more on NUMERIC.
//
// A Go slice or array type is inferred to be a BigQuery repeated field of the
// element type. The element type must be one of the above listed types.
//
// Due to lack of unique native Go type for GEOGRAPHY, there is no schema
// inference to GEOGRAPHY at this time.
//
// Nullable fields are inferred from the NullXXX types, declared in this package:
//
//   STRING      NullString
//   BOOL        NullBool
//   INTEGER     NullInt64
//   FLOAT       NullFloat64
//   TIMESTAMP   NullTimestamp
//   DATE        NullDate
//   TIME        NullTime
//   DATETIME    NullDateTime
//   GEOGRAPHY   NullGeography
//
// For a nullable BYTES field, use the type []byte and tag the field "nullable" (see below).
// For a nullable NUMERIC field, use the type *big.Rat and tag the field "nullable".
//
// A struct field that is of struct type is inferred to be a required field of type
// RECORD with a schema inferred recursively. For backwards compatibility, a field of
// type pointer to struct is also inferred to be required. To get a nullable RECORD
// field, use the "nullable" tag (see below).
//
// InferSchema returns an error if any of the examined fields is of type uint,
// uint64, uintptr, map, interface, complex64, complex128, func, or chan. Future
// versions may handle these cases without error.
//
// Recursively defined structs are also disallowed.
//
// Struct fields may be tagged in a way similar to the encoding/json package.
// A tag of the form
//     bigquery:"name"
// uses "name" instead of the struct field name as the BigQuery field name.
// A tag of the form
//     bigquery:"-"
// omits the field from the inferred schema.
// The "nullable" option marks the field as nullable (not required). It is only
// needed for []byte, *big.Rat and pointer-to-struct fields, and cannot appear on other
// fields. In this example, the Go name of the field is retained:
//     bigquery:",nullable"
func InferSchema(st interface{}) (Schema, error) {
	return inferSchemaReflectCached(reflect.TypeOf(st))
}

var schemaCache sync.Map

type cacheVal struct {
	schema Schema
	err    error
}

func inferSchemaReflectCached(t reflect.Type) (Schema, error) {
	var cv cacheVal
	v, ok := schemaCache.Load(t)
	if ok {
		cv = v.(cacheVal)
	} else {
		s, err := inferSchemaReflect(t)
		cv = cacheVal{s, err}
		schemaCache.Store(t, cv)
	}
	return cv.schema, cv.err
}

func inferSchemaReflect(t reflect.Type) (Schema, error) {
	rec, err := hasRecursiveType(t, nil)
	if err != nil {
		return nil, err
	}
	if rec {
		return nil, fmt.Errorf("bigquery: schema inference for recursive type %s", t)
	}
	return inferStruct(t)
}

func inferStruct(t reflect.Type) (Schema, error) {
	switch t.Kind() {
	case reflect.Ptr:
		if t.Elem().Kind() != reflect.Struct {
			return nil, noStructError{t}
		}
		t = t.Elem()
		fallthrough

	case reflect.Struct:
		return inferFields(t)
	default:
		return nil, noStructError{t}
	}
}

// inferFieldSchema infers the FieldSchema for a Go type
func inferFieldSchema(fieldName string, rt reflect.Type, nullable bool) (*FieldSchema, error) {
	// Only []byte and struct pointers can be tagged nullable.
	if nullable && !(rt == typeOfByteSlice || rt.Kind() == reflect.Ptr && rt.Elem().Kind() == reflect.Struct) {
		return nil, badNullableError{fieldName, rt}
	}
	switch rt {
	case typeOfByteSlice:
		return &FieldSchema{Required: !nullable, Type: BytesFieldType}, nil
	case typeOfGoTime:
		return &FieldSchema{Required: true, Type: TimestampFieldType}, nil
	case typeOfDate:
		return &FieldSchema{Required: true, Type: DateFieldType}, nil
	case typeOfTime:
		return &FieldSchema{Required: true, Type: TimeFieldType}, nil
	case typeOfDateTime:
		return &FieldSchema{Required: true, Type: DateTimeFieldType}, nil
	case typeOfRat:
		// We automatically infer big.Rat values as NUMERIC as we cannot
		// determine precision/scale from the type.  Users who want the
		// larger precision of BIGNUMERIC need to manipulate the inferred
		// schema.
		return &FieldSchema{Required: !nullable, Type: NumericFieldType}, nil
	}
	if ft := nullableFieldType(rt); ft != "" {
		return &FieldSchema{Required: false, Type: ft}, nil
	}
	if isSupportedIntType(rt) || isSupportedUintType(rt) {
		return &FieldSchema{Required: true, Type: IntegerFieldType}, nil
	}
	switch rt.Kind() {
	case reflect.Slice, reflect.Array:
		et := rt.Elem()
		if et != typeOfByteSlice && (et.Kind() == reflect.Slice || et.Kind() == reflect.Array) {
			// Multi dimensional slices/arrays are not supported by BigQuery
			return nil, unsupportedFieldTypeError{fieldName, rt}
		}
		if nullableFieldType(et) != "" {
			// Repeated nullable types are not supported by BigQuery.
			return nil, unsupportedFieldTypeError{fieldName, rt}
		}
		f, err := inferFieldSchema(fieldName, et, false)
		if err != nil {
			return nil, err
		}
		f.Repeated = true
		f.Required = false
		return f, nil
	case reflect.Ptr:
		if rt.Elem().Kind() != reflect.Struct {
			return nil, unsupportedFieldTypeError{fieldName, rt}
		}
		fallthrough
	case reflect.Struct:
		nested, err := inferStruct(rt)
		if err != nil {
			return nil, err
		}
		return &FieldSchema{Required: !nullable, Type: RecordFieldType, Schema: nested}, nil
	case reflect.String:
		return &FieldSchema{Required: !nullable, Type: StringFieldType}, nil
	case reflect.Bool:
		return &FieldSchema{Required: !nullable, Type: BooleanFieldType}, nil
	case reflect.Float32, reflect.Float64:
		return &FieldSchema{Required: !nullable, Type: FloatFieldType}, nil
	default:
		return nil, unsupportedFieldTypeError{fieldName, rt}
	}
}

// inferFields extracts all exported field types from struct type.
func inferFields(rt reflect.Type) (Schema, error) {
	var s Schema
	fields, err := fieldCache.Fields(rt)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		var nullable bool
		for _, opt := range field.ParsedTag.([]string) {
			if opt == nullableTagOption {
				nullable = true
				break
			}
		}
		f, err := inferFieldSchema(field.Name, field.Type, nullable)
		if err != nil {
			return nil, err
		}
		f.Name = field.Name
		s = append(s, f)
	}
	return s, nil
}

// isSupportedIntType reports whether t is an int type that can be properly
// represented by the BigQuery INTEGER/INT64 type.
func isSupportedIntType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
		return true
	default:
		return false
	}
}

// isSupportedIntType reports whether t is a uint type that can be properly
// represented by the BigQuery INTEGER/INT64 type.
func isSupportedUintType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return true
	default:
		return false
	}
}

// typeList is a linked list of reflect.Types.
type typeList struct {
	t    reflect.Type
	next *typeList
}

func (l *typeList) has(t reflect.Type) bool {
	for l != nil {
		if l.t == t {
			return true
		}
		l = l.next
	}
	return false
}

// hasRecursiveType reports whether t or any type inside t refers to itself, directly or indirectly,
// via exported fields. (Schema inference ignores unexported fields.)
func hasRecursiveType(t reflect.Type, seen *typeList) (bool, error) {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false, nil
	}
	if seen.has(t) {
		return true, nil
	}
	fields, err := fieldCache.Fields(t)
	if err != nil {
		return false, err
	}
	seen = &typeList{t, seen}
	// Because seen is a linked list, additions to it from one field's
	// recursive call will not affect the value for subsequent fields' calls.
	for _, field := range fields {
		ok, err := hasRecursiveType(field.Type, seen)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// validateKnownType ensures a type is known (or alias of a known type).
func validateKnownType(in FieldType) (FieldType, error) {
	if _, ok := fieldTypes[in]; !ok {
		// not a defined type, check aliases.
		if resolved, ok := fieldAliases[in]; ok {
			return resolved, nil
		}
		return "", fmt.Errorf("unknown field type (%s)", in)
	}
	return in, nil
}

// SchemaFromJSON takes a native JSON BigQuery table schema definition and converts it to
// a populated Schema.  The native API definition is used by tools such as the BQ CLI and
// https://github.com/GoogleCloudPlatform/protoc-gen-bq-schema.
//
// The expected format is a JSON array of TableFieldSchema objects from the underlying API:
// https://cloud.google.com/bigquery/docs/reference/rest/v2/tables#TableFieldSchema
func SchemaFromJSON(schemaJSON []byte) (Schema, error) {

	// Make sure we actually have some content:
	if len(schemaJSON) == 0 {
		return nil, errEmptyJSONSchema
	}

	var rawSchema []*bq.TableFieldSchema

	if err := json.Unmarshal(schemaJSON, &rawSchema); err != nil {
		return nil, err
	}

	convertedSchema := Schema{}
	for _, f := range rawSchema {
		convField := bqToFieldSchema(f)
		// Normalize the types.
		validType, err := validateKnownType(convField.Type)
		if err != nil {
			return nil, err
		}
		convField.Type = validType
		convertedSchema = append(convertedSchema, convField)
	}
	return convertedSchema, nil
}

type noStructError struct {
	typ reflect.Type
}

func (e noStructError) Error() string {
	return fmt.Sprintf("bigquery: can only infer schema from struct or pointer to struct, not %s", e.typ)
}

type badNullableError struct {
	name string
	typ  reflect.Type
}

func (e badNullableError) Error() string {
	return fmt.Sprintf(`bigquery: field %q of type %s: use "nullable" only for []byte and struct pointers; for all other types, use a NullXXX type`, e.name, e.typ)
}

type unsupportedFieldTypeError struct {
	name string
	typ  reflect.Type
}

func (e unsupportedFieldTypeError) Error() string {
	return fmt.Sprintf("bigquery: field %q: type %s is not supported", e.name, e.typ)
}
