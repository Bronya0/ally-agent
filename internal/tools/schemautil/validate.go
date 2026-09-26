// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package schemautil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// MaxReportedViolations bounds how many problems one report names. Every entry
// costs tokens on the model's next request, and a payload that broke this badly
// needs a rewrite rather than a checklist.
const MaxReportedViolations = 8

// Violation is one way a decoded tool-arguments value breaks its schema. Path is
// relative to the arguments object itself (".files[0].path"; empty for the root).
type Violation struct {
	Path    string
	Message string
}

func (v Violation) Error() string {
	if v.Path == "" {
		return v.Message
	}
	return v.Path + ": " + v.Message
}

// DescribeViolations renders a report as a single line, ready to hang off a
// tool error.
func DescribeViolations(violations []Violation) string {
	parts := make([]string, 0, len(violations))
	for _, violation := range violations {
		parts = append(parts, violation.Error())
	}
	return strings.Join(parts, "; ")
}

// ValidateArgs reports every way args breaks schema, in a deterministic order.
// It exists because the schema is the contract the model was shown: without a
// check here a stray key or an out-of-range value reached the handler and either
// failed with a message that never mentioned the schema, or silently applied a
// default the model never asked for.
//
// Two rules keep it a contract check rather than a general-purpose validator:
//
//   - `null` counts as "not provided": it is skipped by the keyword checks and
//     does not satisfy `required`. That mirrors how the Go decode layer treats
//     an explicit null, so tolerance is not lost for models that spray nulls at
//     optional fields.
//   - A keyword the built-in declarations never use is ignored, never rejected.
func ValidateArgs(schema map[string]any, args []byte) []Violation {
	if schema == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return []Violation{{Message: fmt.Sprintf("arguments are not valid JSON: %v", err)}}
	}
	checker := &checker{}
	checker.check(value, schema, "")
	return checker.violations
}

// CheckPatterns compiles every `pattern` in a schema tree. A pattern that does
// not compile is skipped at validation time (see patternMatches), so without
// this check a typo would silently retire that constraint; hand-written tool
// declarations assert it in their tests instead.
func CheckPatterns(schema map[string]any) error {
	if schema == nil {
		return nil
	}
	if pattern, ok := schema["pattern"].(string); ok {
		if _, err := compiledPattern(pattern); err != nil {
			return err
		}
	}
	if properties, ok := schemaMap(schema["properties"]); ok {
		for _, key := range sortedKeys(properties) {
			if child, ok := schemaMap(properties[key]); ok {
				if err := CheckPatterns(child); err != nil {
					return err
				}
			}
		}
	}
	if itemSchema, ok := schemaMap(schema["items"]); ok {
		if err := CheckPatterns(itemSchema); err != nil {
			return err
		}
	}
	if additional, ok := schemaMap(schema["additionalProperties"]); ok {
		if err := CheckPatterns(additional); err != nil {
			return err
		}
	}
	if notSchema, ok := schemaMap(schema["not"]); ok {
		if err := CheckPatterns(notSchema); err != nil {
			return err
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := schemaSchemas(schema[keyword]); ok {
			for _, variant := range variants {
				if err := CheckPatterns(variant); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type checker struct {
	violations []Violation
}

func (c *checker) add(path, format string, args ...any) {
	if c.full() {
		return
	}
	c.violations = append(c.violations, Violation{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) full() bool { return len(c.violations) >= MaxReportedViolations }

func (c *checker) check(value any, schema map[string]any, path string) {
	if schema == nil || c.full() {
		return
	}
	if expected, ok := schema["type"]; ok && !typeMatches(expected, value) {
		// A wrong type makes every other keyword report noise, so stop here.
		c.add(path, "must be %s, got %s", describeTypes(expected), jsonTypeName(value))
		return
	}
	// The concrete checks (a missing parameter, an unknown property, a string that
	// is too short) run before the summarizing ones (const/enum, the composite
	// keywords). Whoever reads only the first violation — closestAttempt does —
	// gets something to act on instead of a verdict about the whole shape.
	switch typed := value.(type) {
	case map[string]any:
		c.checkObject(typed, schema, path)
	case []any:
		c.checkArray(typed, schema, path)
	case string:
		c.checkString(typed, schema, path)
	case json.Number:
		c.checkNumber(typed, schema, path)
	}
	if constValue, ok := schema["const"]; ok && !valueEquals(constValue, value) {
		c.add(path, "must be %s", renderValue(constValue))
	}
	if values, ok := schemaValues(schema["enum"]); ok && !containsValue(values, value) {
		c.add(path, "must be one of %s, got %s", renderValues(values), renderValue(value))
	}
	if variants, ok := schemaSchemas(schema["allOf"]); ok {
		for _, variant := range variants {
			c.check(value, variant, path)
		}
	}
	if variants, ok := schemaSchemas(schema["anyOf"]); ok {
		c.checkShapes(value, variants, path, false)
	}
	if variants, ok := schemaSchemas(schema["oneOf"]); ok {
		c.checkShapes(value, variants, path, true)
	}
	if notSchema, ok := schemaMap(schema["not"]); ok && c.matches(value, notSchema) {
		if keys, ok := schemaStrings(notSchema["required"]); ok && len(keys) > 0 {
			c.add(path, "must not be combined with %s", renderValues(stringsToValues(keys)))
		} else {
			c.add(path, "this value is not allowed here")
		}
	}
}

func (c *checker) matches(value any, schema map[string]any) bool {
	probe := &checker{}
	probe.check(value, schema, "")
	return len(probe.violations) == 0
}

// shapeProbe summarizes how a value fares against a list of alternative shapes:
// how many of them matched, the violations of the closest one, and the required
// keys of the matched ones (used to explain an overlap).
type shapeProbe struct {
	matched int
	// anchored holds the violations of a failed branch that pins a discriminator
	// (`const`/single-value `enum`) on a field the value carries — the branch the
	// call was clearly aiming at. closest is the fewest-violations fallback used
	// when no branch is anchored.
	anchored   []Violation
	closest    []Violation
	matchedReq [][]string
}

func (c *checker) checkShapes(value any, variants []map[string]any, path string, exactlyOne bool) {
	probe := shapeProbe{}
	for _, variant := range variants {
		sub := &checker{}
		sub.check(value, variant, "")
		if len(sub.violations) == 0 {
			probe.matched++
			if keys, ok := schemaStrings(variant["required"]); ok && len(keys) > 0 {
				probe.matchedReq = append(probe.matchedReq, keys)
			}
			continue
		}
		if anchorsValue(variant, value) && (probe.anchored == nil || len(sub.violations) < len(probe.anchored)) {
			probe.anchored = sub.violations
		}
		if probe.closest == nil || len(sub.violations) < len(probe.closest) {
			probe.closest = sub.violations
		}
	}
	if !exactlyOne {
		if probe.matched == 0 {
			c.add(path, "must match at least one allowed shape; closest attempt: %s", closestAttempt(probe))
		}
		return
	}
	switch {
	case probe.matched == 1:
		return
	case probe.matched == 0:
		c.add(path, "must match exactly one allowed shape; closest attempt: %s", closestAttempt(probe))
	default:
		c.add(path, "must match exactly one allowed shape, but %d matched%s", probe.matched, matchedKeysHint(probe))
	}
}

// closestAttempt explains a failed oneOf/anyOf with the most relevant branch: an
// anchored branch first (the one whose discriminator matches the value), then the
// fewest-violations branch as a fallback. Counting violations alone pointed the
// model at an unrelated branch — {"action":"create","name":"n"}, which is only
// missing schedule, was answered with `action: must be "list"` because the list
// branch happened to be one violation short, so the model would have switched to
// the wrong action.
func closestAttempt(probe shapeProbe) string {
	if len(probe.anchored) > 0 {
		return probe.anchored[0].Error()
	}
	if len(probe.closest) == 0 {
		return "no allowed shape applies"
	}
	return probe.closest[0].Error()
}

// anchorsValue reports whether a branch is the one this value was aiming at: the
// branch pins a discriminator (`const`, or a single-value `enum`) on a field the
// value carries with that same value.
func anchorsValue(variant map[string]any, value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	properties, ok := schemaMap(variant["properties"])
	if !ok {
		return false
	}
	for key, raw := range properties {
		property, ok := schemaMap(raw)
		if !ok {
			continue
		}
		if constValue, ok := property["const"]; ok {
			if actual, present := object[key]; present && valueEquals(constValue, actual) {
				return true
			}
			continue
		}
		if values, ok := schemaValues(property["enum"]); ok && len(values) == 1 {
			if actual, present := object[key]; present && valueEquals(values[0], actual) {
				return true
			}
		}
	}
	return false
}

func matchedKeysHint(probe shapeProbe) string {
	if len(probe.matchedReq) == 0 {
		return ""
	}
	parts := make([]string, 0, len(probe.matchedReq))
	for _, keys := range probe.matchedReq {
		parts = append(parts, strings.Join(keys, "+"))
	}
	return " (also satisfied the shape requiring " + strings.Join(parts, " and ") + ")"
}

func (c *checker) checkObject(object map[string]any, schema map[string]any, path string) {
	properties, _ := schemaMap(schema["properties"])
	if keys, ok := schemaStrings(schema["required"]); ok {
		for _, key := range keys {
			value, present := object[key]
			if !present || value == nil {
				c.add(path, "missing required parameter %q", key)
			}
		}
	}
	switch additional := schema["additionalProperties"].(type) {
	case bool:
		if !additional {
			for _, key := range unsupportedKeys(object, properties) {
				c.add(path, "unsupported parameter %q (%s)", key, supportedHint(properties))
			}
		}
	case map[string]any:
		for _, key := range unsupportedKeys(object, properties) {
			c.check(object[key], additional, joinPath(path, key))
		}
	}
	for _, key := range sortedKeys(properties) {
		value, present := object[key]
		if !present || value == nil {
			continue
		}
		sub, ok := schemaMap(properties[key])
		if !ok {
			continue
		}
		c.check(value, sub, joinPath(path, key))
	}
}

func (c *checker) checkArray(items []any, schema map[string]any, path string) {
	if minimum, ok := schemaNumber(schema["minItems"]); ok && float64(len(items)) < minimum {
		c.add(path, "must contain at least %d items (got %d)", int(minimum), len(items))
	}
	if maximum, ok := schemaNumber(schema["maxItems"]); ok && float64(len(items)) > maximum {
		c.add(path, "must contain at most %d items (got %d)", int(maximum), len(items))
	}
	itemSchema, ok := schemaMap(schema["items"])
	if !ok {
		return
	}
	for i, item := range items {
		c.check(item, itemSchema, fmt.Sprintf("%s[%d]", path, i))
	}
}

func (c *checker) checkString(value string, schema map[string]any, path string) {
	length := utf8.RuneCountInString(value)
	if minimum, ok := schemaNumber(schema["minLength"]); ok && float64(length) < minimum {
		c.add(path, "must be at least %d characters (got %d)", int(minimum), length)
	}
	if maximum, ok := schemaNumber(schema["maxLength"]); ok && float64(length) > maximum {
		c.add(path, "must be at most %d characters (got %d)", int(maximum), length)
	}
	if pattern, ok := schema["pattern"].(string); ok && !patternMatches(pattern, value) {
		c.add(path, "must match the pattern %s", pattern)
	}
}

func (c *checker) checkNumber(value json.Number, schema map[string]any, path string) {
	number, err := value.Float64()
	if err != nil {
		return
	}
	if minimum, ok := schemaNumber(schema["minimum"]); ok && number < minimum {
		c.add(path, "must be >= %s (got %s)", formatNumber(minimum), value.String())
	}
	if maximum, ok := schemaNumber(schema["maximum"]); ok && number > maximum {
		c.add(path, "must be <= %s (got %s)", formatNumber(maximum), value.String())
	}
}

func unsupportedKeys(object, properties map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key, value := range object {
		if value == nil {
			continue
		}
		if _, declared := properties[key]; declared {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func supportedHint(properties map[string]any) string {
	if len(properties) == 0 {
		return "this tool takes no parameters"
	}
	return "supported: " + strings.Join(sortedKeys(properties), ", ")
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func schemaMap(raw any) (map[string]any, bool) {
	object, ok := raw.(map[string]any)
	return object, ok
}

func schemaSchemas(raw any) ([]map[string]any, bool) {
	items, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	schemas := make([]map[string]any, 0, len(items))
	for _, item := range items {
		schema, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		schemas = append(schemas, schema)
	}
	return schemas, true
}

// schemaStrings reads a keyword whose value is a list of names (`required`).
func schemaStrings(raw any) ([]string, bool) {
	switch values := raw.(type) {
	case []string:
		return values, true
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			name, ok := value.(string)
			if !ok {
				return nil, false
			}
			out = append(out, name)
		}
		return out, true
	}
	return nil, false
}

// schemaValues reads a keyword whose value is a list of literal values (`enum`).
func schemaValues(raw any) ([]any, bool) {
	switch values := raw.(type) {
	case []any:
		return values, true
	case []string:
		return stringsToValues(values), true
	}
	return nil, false
}

func stringsToValues(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func schemaNumber(raw any) (float64, bool) { return numericValue(raw) }

func numericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	}
	return 0, false
}

func containsValue(values []any, value any) bool {
	for _, candidate := range values {
		if valueEquals(candidate, value) {
			return true
		}
	}
	return false
}

func valueEquals(expected, value any) bool {
	switch want := expected.(type) {
	case string:
		got, ok := value.(string)
		return ok && got == want
	case bool:
		got, ok := value.(bool)
		return ok && got == want
	case nil:
		return value == nil
	}
	wantNumber, wantOK := numericValue(expected)
	gotNumber, gotOK := numericValue(value)
	return wantOK && gotOK && wantNumber == gotNumber
}

func typeMatches(expected any, value any) bool {
	switch want := expected.(type) {
	case string:
		return typeNameMatches(want, value)
	case []any:
		for _, item := range want {
			if name, ok := item.(string); ok && typeNameMatches(name, value) {
				return true
			}
		}
		return false
	}
	// An unknown shape must not block the call.
	return true
}

func typeNameMatches(want string, value any) bool {
	switch want {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		parsed, err := number.Float64()
		return err == nil && parsed == math.Trunc(parsed)
	}
	return true
}

func describeTypes(expected any) string {
	switch want := expected.(type) {
	case string:
		return want
	case []any:
		names := make([]string, 0, len(want))
		for _, item := range want {
			if name, ok := item.(string); ok {
				names = append(names, name)
			}
		}
		return strings.Join(names, " or ")
	}
	return "the declared type"
}

func jsonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "value"
}

func renderValue(value any) string {
	if value == nil {
		return "null"
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}

func renderValues(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, renderValue(value))
	}
	return strings.Join(parts, ", ")
}

func formatNumber(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

var patternCache sync.Map

func compiledPattern(pattern string) (*regexp.Regexp, error) {
	if cached, ok := patternCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	patternCache.Store(pattern, compiled)
	return compiled, nil
}

// patternMatches is an unanchored search, matching JSON Schema semantics and the
// Go regexp dialect the built-in declarations are written in. A pattern that
// does not compile cannot be enforced, so it passes instead of blocking the
// call; CheckPatterns exists to keep that from happening unnoticed.
func patternMatches(pattern, value string) bool {
	compiled, err := compiledPattern(pattern)
	if err != nil {
		return true
	}
	return compiled.MatchString(value)
}
