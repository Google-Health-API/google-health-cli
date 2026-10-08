// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Print outputs data in the specified format to stdout.
func Print(format string, data json.RawMessage) error {
	switch strings.ToLower(format) {
	case "json":
		return PrintJSON(data)
	case "table":
		return PrintTable(data)
	case "csv":
		return PrintCSV(data)
	default:
		return PrintJSON(data)
	}
}

// PrintToFile writes the formatted data to a file and prints a summary to stdout.
// The summary includes row count, columns (for CSV), and a preview of the first few rows.
func PrintToFile(format string, data json.RawMessage, filePath string) error {
	// Format the data into a buffer.
	var buf bytes.Buffer
	switch strings.ToLower(format) {
	case "csv":
		rows := flattenRows(extractRows(data))
		if len(rows) == 0 {
			// Empty tabular result → write an empty CSV file (never a JSON dump);
			// non-tabular → JSON fallback so the file stays readable.
			if isListShaped(data) {
				fwarnDroppedSignals(os.Stderr, data)
				return writeFileWithSummary(filePath, nil, format, 0, nil)
			}
			return writeFileWithSummary(filePath, data, format, 0, nil)
		}
		fwarnDroppedSignals(os.Stderr, data)
		if err := printRowsAsCSV(&buf, rows); err != nil {
			return err
		}
		keys := sortedKeys(rows)
		if err := os.WriteFile(filePath, buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", filePath, err)
		}
		// Print summary with preview.
		fmt.Fprintf(os.Stdout, "Wrote %d rows to %s\n\nColumns: %s\nPreview:\n", len(rows), filePath, strings.Join(keys, ", "))
		// Print header + up to 3 rows as preview.
		preview := &bytes.Buffer{}
		previewRows := rows
		if len(previewRows) > 3 {
			previewRows = previewRows[:3]
		}
		printRowsAsCSV(preview, previewRows)
		fmt.Fprint(os.Stdout, preview.String())
		return nil
	case "table":
		rows := extractRows(data)
		if len(rows) > 0 {
			printRowsAsTable(&buf, rows)
		} else {
			var v interface{}
			json.Unmarshal(data, &v)
			enc := json.NewEncoder(&buf)
			enc.SetIndent("", "  ")
			enc.Encode(v)
		}
	default: // json
		var v interface{}
		json.Unmarshal(data, &v)
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		enc.Encode(v)
	}

	count := countDataPoints(data)
	return writeFileWithSummary(filePath, buf.Bytes(), format, count, nil)
}

func writeFileWithSummary(filePath string, content []byte, format string, count int, keys []string) error {
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", filePath, err)
	}
	if count > 0 {
		fmt.Fprintf(os.Stdout, "Wrote %d data points to %s\n", count, filePath)
	} else {
		fmt.Fprintf(os.Stdout, "Wrote %s\n", filePath)
	}
	return nil
}

func extractRows(data json.RawMessage) []map[string]interface{} {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		var rows []map[string]interface{}
		json.Unmarshal(data, &rows)
		return rows
	}
	for _, key := range []string{"dataPoints", "rollupDataPoints", "data", "items"} {
		if raw, ok := obj[key]; ok {
			var rows []map[string]interface{}
			if err := json.Unmarshal(raw, &rows); err == nil && len(rows) > 0 {
				return rows
			}
		}
	}
	var rows []map[string]interface{}
	json.Unmarshal(data, &rows)
	return rows
}

func sortedKeys(rows []map[string]interface{}) []string {
	keySet := make(map[string]bool)
	for _, row := range rows {
		for k := range row {
			keySet[k] = true
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func countDataPoints(data json.RawMessage) int {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		var arr []json.RawMessage
		if json.Unmarshal(data, &arr) == nil {
			return len(arr)
		}
		return 0
	}
	for _, key := range []string{"dataPoints", "rollupDataPoints", "data", "items"} {
		if raw, ok := obj[key]; ok {
			var arr []json.RawMessage
			if json.Unmarshal(raw, &arr) == nil {
				return len(arr)
			}
		}
	}
	return 0
}

// PrintJSON outputs pretty-printed JSON.
func PrintJSON(data json.RawMessage) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		// If it's not valid JSON, print raw.
		fmt.Println(string(data))
		return nil
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// PrintTable outputs data as an aligned table. extractRows handles both
// object responses (dataPoints/rollupDataPoints/...) and the bare arrays
// that simplified rollup output produces; anything without rows falls back
// to JSON.
func PrintTable(data json.RawMessage) error {
	rows := extractRows(data)
	if len(rows) == 0 {
		// Non-tabular or empty: table mode is for humans, so the JSON fallback
		// stays readable and keeps _hints visible.
		return PrintJSON(data)
	}
	fwarnDroppedSignals(os.Stderr, data)
	return printRowsAsTable(os.Stdout, rows)
}

func printRowsAsTable(w io.Writer, rows []map[string]interface{}) error {
	if len(rows) == 0 {
		return nil
	}

	// Collect all keys.
	keySet := make(map[string]bool)
	for _, row := range rows {
		for k := range row {
			keySet[k] = true
		}
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Calculate column widths.
	widths := make(map[string]int)
	for _, k := range keys {
		widths[k] = len(strings.ToUpper(k))
	}
	for _, row := range rows {
		for _, k := range keys {
			val := formatValue(row[k])
			if len(val) > widths[k] {
				widths[k] = len(val)
			}
		}
	}

	// Print header.
	for i, k := range keys {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		fmt.Fprintf(w, "%-*s", widths[k], strings.ToUpper(k))
	}
	fmt.Fprintln(w)

	// Print rows.
	for _, row := range rows {
		for i, k := range keys {
			if i > 0 {
				fmt.Fprint(w, "  ")
			}
			fmt.Fprintf(w, "%-*s", widths[k], formatValue(row[k]))
		}
		fmt.Fprintln(w)
	}

	return nil
}

func formatValue(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%.2f", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case map[string]interface{}:
		b, _ := json.Marshal(val)
		return string(b)
	case []interface{}:
		b, _ := json.Marshal(val)
		return string(b)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// PrintCSV outputs data as CSV, keeping the stream pure for dataframe readers:
// an empty list-shaped result emits nothing (never a JSON object dumped into the
// CSV stream), and a non-tabular response (e.g. auth status) falls back to JSON.
// _hints and a leftover nextPageToken are surfaced on stderr, not in the data.
func PrintCSV(data json.RawMessage) error {
	rows := extractRows(data)
	if len(rows) == 0 {
		if isListShaped(data) {
			fwarnDroppedSignals(os.Stderr, data)
			return nil // empty tabular result → empty CSV, not a JSON dump
		}
		return PrintJSON(data) // non-tabular: nothing to tabulate
	}
	fwarnDroppedSignals(os.Stderr, data)
	return printRowsAsCSV(os.Stdout, flattenRows(rows))
}

// isListShaped reports whether data is a tabular response — a top-level array,
// or an object carrying a row-array key — even when that array is empty. It
// distinguishes an empty result (emit an empty CSV) from a non-tabular object
// such as auth status (fall back to JSON).
func isListShaped(data json.RawMessage) bool {
	var arr []json.RawMessage
	if json.Unmarshal(data, &arr) == nil {
		return true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return false
	}
	for _, key := range []string{"dataPoints", "rollupDataPoints", "data", "items"} {
		if _, ok := obj[key]; ok {
			return true
		}
	}
	return false
}

// fwarnDroppedSignals surfaces _hints and a leftover nextPageToken on w when
// tabular output renders only the rows. Without this, CSV/table mode silently
// hides the truncation signal that JSON mode carries in-band — an agent reading
// the CSV stream would misreport a --limit-capped result as complete.
func fwarnDroppedSignals(w io.Writer, data json.RawMessage) {
	var obj struct {
		Hints         []string `json:"_hints"`
		NextPageToken string   `json:"nextPageToken"`
		APIVersion    string   `json:"_api_version"`
	}
	if json.Unmarshal(data, &obj) != nil {
		return
	}
	// The default GA channel stays quiet; a pre-GA channel is called out so a
	// table/csv reader knows the rows came from a beta surface.
	if obj.APIVersion != "" && obj.APIVersion != "v4" {
		fmt.Fprintf(w, "api_version: %s\n", obj.APIVersion)
	}
	for _, h := range obj.Hints {
		fmt.Fprintf(w, "hint: %s\n", h)
	}
	if obj.NextPageToken != "" {
		fmt.Fprintf(w, "nextPageToken: %s\n", obj.NextPageToken)
	}
}

// flattenRows expands nested objects into dot-separated column names
// (e.g., metricsSummary.caloriesKcal) and removes internal fields like _hints.
func flattenRows(rows []map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		flat := make(map[string]interface{})
		flattenMap("", row, flat)
		result = append(result, flat)
	}
	return result
}

func flattenMap(prefix string, m map[string]interface{}, out map[string]interface{}) {
	for k, v := range m {
		if strings.HasPrefix(k, "_") {
			continue // skip _hints and other internal fields
		}
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]interface{}:
			flattenMap(key, val, out)
		case []interface{}:
			// Keep arrays as JSON strings — can't flatten variable-length arrays into columns.
			b, _ := json.Marshal(val)
			out[key] = string(b)
		default:
			out[key] = v
		}
	}
}

func printRowsAsCSV(w io.Writer, rows []map[string]interface{}) error {
	if len(rows) == 0 {
		return nil
	}

	keySet := make(map[string]bool)
	for _, row := range rows {
		for k := range row {
			keySet[k] = true
		}
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	cw := csv.NewWriter(w)
	defer cw.Flush()

	// Header.
	if err := cw.Write(keys); err != nil {
		return err
	}

	// Rows.
	for _, row := range rows {
		record := make([]string, len(keys))
		for i, k := range keys {
			record[i] = formatValuePrecise(row[k])
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}

	return nil
}

// formatValuePrecise is formatValue without the 2-decimal rounding: CSV is a
// data-export format, so float values must round-trip at full precision.
func formatValuePrecise(v interface{}) string {
	if f, ok := v.(float64); ok && f != float64(int64(f)) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return formatValue(v)
}
