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
	typeOf := reflect.TypeOf(shape)
	names := make(map[string]string, typeOf.NumField())
	for i := 0; i < typeOf.NumField(); i++ {
		field := typeOf.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		names[strings.ToLower(name)] = name
	}
	return names
}
