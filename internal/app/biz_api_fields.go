// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// encoding/json accepts alternate casing and silently drops unknown keys, but
// patch presence/merge rules use canonical keys. Reject both before either can
// interpret credentials differently: a misspelled key must not look like a
// saved one. Names come from the DTO rather than another field table; readable
// lists the read-only keys a round-tripped GET response carries.
func apiValidateFieldNames(fields map[string]json.RawMessage, shape any, readable ...string) error {
	canonical := apiFieldNames(shape)
	for _, name := range readable {
		canonical[strings.ToLower(name)] = name
	}
	for key := range fields {
		name, known := canonical[strings.ToLower(key)]
		switch {
		case !known:
			return fmt.Errorf("unknown JSON field %q", key)
		case name != key:
			return fmt.Errorf("use canonical JSON field %q", name)
		}
	}
	return nil
}

// apiFieldNames maps lower-case JSON names to their canonical spelling.
func apiFieldNames(shape any) map[string]string {
	fields := apiFieldDefinitions(reflect.TypeOf(shape))
	names := make(map[string]string, len(fields))
	for name := range fields {
		names[strings.ToLower(name)] = name
	}
	return names
}

func apiFieldDefinitions(typeOf reflect.Type) map[string]reflect.StructField {
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	fields := make(map[string]reflect.StructField)
	for _, field := range reflect.VisibleFields(typeOf) {
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			// Promoted fields are already included by VisibleFields.
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if field.Anonymous && embedded.Kind() == reflect.Struct {
				continue
			}
			name = field.Name
		}
		fields[name] = field
	}
	return fields
}

// Validate before decoding so invalid names cannot partially change a target.
// Maps and raw JSON retain their handler-specific patch/extension validation.
func apiValidateBodyFields(raw json.RawMessage, typeOf reflect.Type, readable ...string) error {
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	switch typeOf.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if err := apiValidateFieldNames(fields, reflect.Zero(typeOf).Interface(), readable...); err != nil {
			return err
		}
		definitions := apiFieldDefinitions(typeOf)
		for name, value := range fields {
			if field, ok := definitions[name]; ok {
				if err := apiValidateBodyFields(value, field.Type); err != nil {
					return err
				}
			}
		}
	case reflect.Slice, reflect.Array:
		// []byte includes json.RawMessage, which is validated by its handler.
		if typeOf.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		for _, item := range items {
			if err := apiValidateBodyFields(item, typeOf.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
