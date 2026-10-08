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

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"ghealth/pkg/client"
	"ghealth/pkg/output"
	"ghealth/pkg/types"
	"github.com/spf13/cobra"
)

// listMaxPageSize is the largest pageSize the API accepts for a data type's
// list, from the registry (exercise/sleep are capped at 25, others 10000).
// Requesting more than the cap is rejected, so list pagination sizes pages
// within it. Falls back to the permissive default for an unknown type.
func listMaxPageSize(dataType string) int {
	if dt := types.Get(dataType); dt != nil {
		return dt.MaxPageSize()
	}
	return 10000
}

// ─── Data Command ────────────────────────────────────────────────

var dataCmd = &cobra.Command{
	Use:   "data",
	Short: "Query and manage health data",
	Long: fmt.Sprintf(`Access %d health data types. Use daily-rollup for totals (steps, distance, floors).
Use list for individual readings (heart rate, weight, SpO2, exercise, sleep).
Types marked [beta] need --api-version v4beta.
Run 'ghealth schema types' for the full live list.`, len(types.Registry)),
	// Unknown types would otherwise fall through to Cobra's help output with
	// exit 0 (or an "unknown flag" error when flags follow), which an agent
	// cannot tell apart from success. Accept any args/flags here and return a
	// structured validation error from RunE instead.
	FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
	Args:               cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return client.NewValidationError(
			fmt.Sprintf("unknown data type: %s", args[0]),
			"Run 'ghealth schema types' to list supported types (types marked [beta] need --api-version v4beta)",
		)
	},
}

func init() {
	rootCmd.AddCommand(dataCmd)
	for _, id := range types.IDs() {
		dataCmd.AddCommand(newTypeCommand(types.Get(id)))
	}
}

// ─── Type Subcommand ─────────────────────────────────────────────

func newTypeCommand(dt *types.DataType) *cobra.Command {
	cmd := &cobra.Command{
		Use:   dt.ID,
		Short: dt.Description,
		RunE: func(cmd *cobra.Command, args []string) error {
			// When invoked without a subcommand (or with an invalid one),
			// return a structured error instead of help text.
			ops := strings.Join(dt.Operations, ", ")
			return client.NewValidationError(
				fmt.Sprintf("no operation specified for %s", dt.ID),
				fmt.Sprintf("Available operations: %s. Example: ghealth data %s %s", ops, dt.ID, dt.Operations[0]),
			)
		},
	}
	// Channel gating: the type command carries the type's channel, and each
	// operation carries its own only when it differs (OpChannels override).
	markChannel(cmd, dt.Channel)
	for _, op := range dt.Operations {
		var opCmd *cobra.Command
		switch op {
		case "list":
			opCmd = newListCommand(dt)
		case "get":
			opCmd = newGetCommand(dt)
		case "create":
			opCmd = newCreateCommand(dt)
		case "update":
			opCmd = newUpdateCommand(dt)
		case "delete":
			opCmd = newDeleteCommand(dt)
		case "rollup":
			opCmd = newRollupCommand(dt)
		case "daily-rollup":
			opCmd = newDailyRollupCommand(dt)
		case "reconcile":
			opCmd = newReconcileCommand(dt)
		case "export-tcx":
			opCmd = newExportTCXCommand(dt)
		default:
			continue
		}
		if ch := dt.OperationChannel(op); ch != dt.OperationChannel("") {
			markChannel(opCmd, ch)
		}
		cmd.AddCommand(opCmd)
	}
	return cmd
}

// ─── Date & Filter Helpers ───────────────────────────────────────

func parseDate(s string) (string, error) {
	switch strings.ToLower(s) {
	case "today":
		return time.Now().In(activeLocation()).Format("2006-01-02") + "T00:00:00", nil
	case "yesterday":
		return time.Now().In(activeLocation()).AddDate(0, 0, -1).Format("2006-01-02") + "T00:00:00", nil
	}
	if _, err := time.Parse("2006-01-02", s); err == nil {
		return s + "T00:00:00", nil
	}
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return s, nil
	}
	if _, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return s, nil
	}
	return "", client.NewValidationError(
		fmt.Sprintf("invalid date format: %s", s),
		"Use YYYY-MM-DD, ISO 8601, 'today', or 'yesterday'",
	)
}

// parseCivilDate converts a date string to a CivilDateTime object for rollup requests.
// The Health API v4 rollup endpoints expect: {"date": {"year": N, "month": N, "day": N}}
func parseCivilDate(s string) (map[string]interface{}, error) {
	dateStr, err := parseDate(s)
	if err != nil {
		return nil, err
	}
	// Parse the date portion from the ISO-ish string
	t, parseErr := time.Parse("2006-01-02", dateStr[:10])
	if parseErr != nil {
		t, parseErr = time.Parse(time.RFC3339, dateStr)
		if parseErr != nil {
			t, _ = time.Parse("2006-01-02T15:04:05", dateStr)
		}
	}
	return map[string]interface{}{
		"date": map[string]int{
			"year":  t.Year(),
			"month": int(t.Month()),
			"day":   t.Day(),
		},
	}, nil
}

// parsePhysicalTime converts a date string to an RFC-3339 UTC instant for the
// rollUp endpoint, whose range is a physical-time Interval (not a civil date).
// A bare date / today / yesterday names the user's LOCAL calendar day, so it is
// anchored at local midnight and converted to UTC (see anchorLocalToUTC).
func parsePhysicalTime(s string) (string, error) {
	dateStr, err := parseDate(s)
	if err != nil {
		return "", err
	}
	return anchorLocalToUTC(dateStr), nil
}

// parsePhysicalEndTime resolves a --to value into the exclusive RFC-3339 upper
// bound of a rollUp range. Like parseEndDate, a value naming a whole calendar
// day (YYYY-MM-DD, "today", "yesterday") is advanced to the start of the next
// local day so the range is inclusive of the named day; an explicit timestamp
// is a precise bound and passes through unchanged.
func parsePhysicalEndTime(s string) (string, error) {
	dateStr, err := parseEndDate(s)
	if err != nil {
		return "", err
	}
	return anchorLocalToUTC(dateStr), nil
}

// parseCivilEndDate resolves a --to value into the exclusive civil upper bound
// of a dailyRollUp range. A bare-day value is advanced to the next day so the
// closed-open range is inclusive of the named day.
func parseCivilEndDate(s string) (map[string]interface{}, error) {
	if d, ok := bareDate(s); ok {
		next := d.AddDate(0, 0, 1)
		return map[string]interface{}{
			"date": map[string]int{
				"year":  next.Year(),
				"month": int(next.Month()),
				"day":   next.Day(),
			},
		}, nil
	}
	return parseCivilDate(s)
}

// anchorLocalToUTC turns a filter date into the UTC instant to compare a
// physical-time (true-instant) field against. A value that already carries a
// timezone (Z or ±offset) is converted to UTC as given. A zone-less value is
// interpreted as LOCAL wall-clock time — the configured profile timezone when
// set (see activeLocation), else machine-local — and converted to UTC. So a
// bare date means the user's local midnight, matching how civil/interval
// filters behave, rather than UTC midnight. On a UTC host this is a no-op.
func anchorLocalToUTC(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	loc := activeLocation()
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, loc); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s // not a recognized shape; leave for downstream handling
}

// usesPhysicalTime reports whether a data type filters on a true-instant
// (UTC) field, for which a bare date must be anchored at the user's local day.
func usesPhysicalTime(timeField string) bool {
	return timeField == types.TimeFieldSample || timeField == types.TimeFieldPhysicalIntervalStart
}

// usesCivilDateTime reports whether a data type filters on a civil datetime
// field, which the API rejects when the value carries a timezone designator
// (Z or ±offset). Daily-summary types compare plain dates and are exempt.
func usesCivilDateTime(dt *types.DataType) bool {
	if dt.FilterField != "" {
		return strings.Contains(dt.FilterField, "civil")
	}
	return dt.TimeField == types.TimeFieldInterval
}

// rejectZonedCivil returns a validation error when a zoned timestamp targets
// a civil filter field — failing locally with guidance beats an API 400.
func rejectZonedCivil(dt *types.DataType, flag, raw, parsed string) error {
	if !usesCivilDateTime(dt) || !types.HasTimezone(parsed) {
		return nil
	}
	return client.NewValidationError(
		fmt.Sprintf("%s %q: %s filters on a civil-time field (%s), which does not accept a timezone designator",
			flag, raw, dt.ID, dt.FilterPath()),
		"Pass a local date/time without offset (e.g. 2026-03-28T10:00:00) or a bare date",
	)
}

func buildFilter(dt *types.DataType, from, to, rawFilter string) (string, error) {
	if rawFilter != "" {
		return rawFilter, nil
	}
	var parts []string
	if from != "" {
		parsed, err := parseDate(from)
		if err != nil {
			return "", err
		}
		// Physical-time fields compare against true UTC instants; anchor a bare
		// date at the user's local day so it lines up with civil/interval types.
		if usesPhysicalTime(dt.TimeField) {
			parsed = anchorLocalToUTC(parsed)
		} else if err := rejectZonedCivil(dt, "--from", from, parsed); err != nil {
			return "", err
		}
		if f := dt.FilterFrom(parsed); f != "" {
			parts = append(parts, f)
		}
	}
	if to != "" && dt.TimeField == types.TimeFieldPhysicalIntervalStart {
		fmt.Fprintf(os.Stderr,
			"warning: %s does not support end-time filtering; --to is ignored\n", dt.ID)
	}
	if to != "" {
		parsed, err := parseEndDate(to)
		if err != nil {
			return "", err
		}
		if usesPhysicalTime(dt.TimeField) {
			parsed = anchorLocalToUTC(parsed)
		} else if err := rejectZonedCivil(dt, "--to", to, parsed); err != nil {
			return "", err
		}
		if f := dt.FilterTo(parsed); f != "" {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, " AND "), nil
}

// parseEndDate resolves a --to value into the exclusive upper bound consumed by
// FilterTo. The API only supports "<" (never "<="), so a value that names a
// whole calendar day (YYYY-MM-DD, "today", "yesterday") is advanced to the
// start of the *next* day, making the range inclusive of the named day — which
// is what "end date" implies. A value that already carries a time-of-day is a
// precise bound and is passed through unchanged.
func parseEndDate(s string) (string, error) {
	if d, ok := bareDate(s); ok {
		return d.AddDate(0, 0, 1).Format("2006-01-02") + "T00:00:00", nil
	}
	return parseDate(s)
}

// bareDate reports whether s denotes a whole calendar day (YYYY-MM-DD, "today",
// or "yesterday") and, if so, returns that day.
func bareDate(s string) (time.Time, bool) {
	switch strings.ToLower(s) {
	case "today":
		now := time.Now().In(activeLocation())
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), true
	case "yesterday":
		now := time.Now().In(activeLocation()).AddDate(0, 0, -1)
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// ─── Request Execution ──────────────────────────────────────────

func doDryRun(req *client.Request) error {
	c := newClient()
	data, err := c.DryRun(req)
	if err != nil {
		return client.NewAPIError(0, fmt.Sprintf("dry run failed: %v", err), "")
	}
	return output.PrintJSON(data)
}

func doRequest(req *client.Request) error {
	c := newClient()
	resp, err := c.Do(req)
	if err != nil {
		if cliErr, ok := err.(*client.CLIError); ok {
			return cliErr
		}
		return client.NewAPIError(0, err.Error(), "")
	}
	return printOutput(resp.Body)
}

// printOutput routes to file or stdout based on --output flag.
func printOutput(data json.RawMessage) error {
	if flagOutput != "" {
		return output.PrintToFile(getFormat(), data, flagOutput)
	}
	return output.Print(getFormat(), data)
}

// dataListOpts holds the context needed for auto-pagination, simplification, and hints.
type dataListOpts struct {
	dataType    string
	operation   string
	limit       int
	pageToken   string
	from, to    string
	sleepDetail bool
}

// listPageCap bounds auto-pagination as a safety net against a server that
// never stops returning a nextPageToken. Reaching it does not silently
// truncate: executeDataList surfaces the continuation token and a warning so
// the result is never misreported as complete.
const listPageCap = 1000

// collectPages accumulates data points across pages until it has at least
// `limit` points or the server stops returning a nextPageToken. Pagination
// begins from startToken ("" for the first page, or a caller-supplied
// --page-token to resume). fetchPage is called with the page token and the
// number of rows still wanted; it returns that page's points and the token for
// the following page.
//
// Passing `want` lets fetchPage size each request to the exact remainder (within
// the API's per-type cap), so the final page lands on the --limit boundary and
// the returned token resumes losslessly — no rows skipped, none repeated.
//
// The returned remainingToken is non-empty whenever pagination stopped with the
// server still offering more data — the limit was reached or the page-cap safety
// net was hit. Only when the data is genuinely exhausted is it "". Callers use
// it to signal incomplete results and to support --page-token resumption.
func collectPages(limit, pageCap int, startToken string, fetchPage func(pageToken string, want int) ([]json.RawMessage, string, error)) (points []json.RawMessage, remainingToken string, err error) {
	var all []json.RawMessage
	token := startToken
	for page := 0; page < pageCap; page++ {
		want := limit - len(all)
		if want <= 0 {
			return all, token, nil
		}
		pts, next, err := fetchPage(token, want)
		if err != nil {
			return nil, "", err
		}
		all = append(all, pts...)
		// Enough to satisfy the limit, or no more pages. Surface the token so
		// a limit-stop with more data available is never mistaken for a
		// complete result.
		if len(all) >= limit || next == "" {
			return all, next, nil
		}
		token = next
	}
	// Hit the safety cap with the server still offering more data.
	return all, token, nil
}

// limitTruncated reports whether a list result was cut off by --limit while
// more data exists — either points beyond the limit were fetched and dropped,
// or the server still offered a continuation token at the limit. A token with
// fewer points than the limit is a page-cap stop, not a --limit truncation.
func limitTruncated(fetched, limit int, remainingToken string) bool {
	if fetched > limit {
		return true
	}
	return fetched == limit && remainingToken != ""
}

// executeDataList fetches data with auto-pagination, simplifies, and injects hints.
// It merges all pages into a single response and caps at opts.limit total results.
func executeDataList(req *client.Request, opts dataListOpts) error {
	c := newClient()
	totalLimit := opts.limit
	if totalLimit <= 0 {
		totalLimit = 500 // sensible default cap
	}

	maxPage := listMaxPageSize(opts.dataType)
	fetchPage := func(pageToken string, want int) ([]json.RawMessage, string, error) {
		if req.Query == nil {
			req.Query = url.Values{}
		}
		// Request exactly the rows still needed (within the per-type cap) so the
		// page boundary aligns with --limit and the surfaced nextPageToken
		// resumes losslessly.
		pageSize := want
		if pageSize > maxPage {
			pageSize = maxPage
		}
		req.Query.Set("pageSize", strconv.Itoa(pageSize))
		if pageToken != "" {
			req.Query.Set("pageToken", pageToken)
		} else {
			req.Query.Del("pageToken")
		}
		resp, err := c.Do(req)
		if err != nil {
			if cliErr, ok := err.(*client.CLIError); ok {
				return nil, "", cliErr
			}
			return nil, "", client.NewAPIError(0, err.Error(), "")
		}
		var pageData struct {
			DataPoints    []json.RawMessage `json:"dataPoints"`
			NextPageToken string            `json:"nextPageToken"`
		}
		if err := json.Unmarshal(resp.Body, &pageData); err != nil {
			return nil, "", client.NewAPIError(0,
				fmt.Sprintf("failed to parse API response: %v", err),
				"The server returned a response that could not be decoded.")
		}
		return pageData.DataPoints, pageData.NextPageToken, nil
	}

	allPoints, remainingToken, err := collectPages(totalLimit, listPageCap, opts.pageToken, fetchPage)
	if err != nil {
		return err
	}

	fetched := len(allPoints)
	truncated := limitTruncated(fetched, totalLimit, remainingToken)
	cappedByPageCap := remainingToken != "" && fetched < totalLimit

	// Cap to limit.
	if fetched > totalLimit {
		allPoints = allPoints[:totalLimit]
	}

	// Rebuild as a single response. If pagination stopped early with more data
	// available, preserve the continuation token and warn so the result is not
	// mistaken for the complete set.
	mergedObj := map[string]interface{}{"dataPoints": allPoints}
	if remainingToken != "" {
		mergedObj["nextPageToken"] = remainingToken
	}
	if cappedByPageCap {
		fmt.Fprintf(os.Stderr,
			"warning: stopped after %d pages with more data still available; "+
				"continue with --page-token %s or narrow --from/--to\n",
			listPageCap, remainingToken)
	}
	merged, _ := json.Marshal(mergedObj)

	// Simplify.
	var simplified json.RawMessage
	if opts.dataType == "sleep" {
		simplified = output.SimplifySleepResponse(merged, opts.sleepDetail, flagRaw)
	} else {
		simplified = output.SimplifyResponse(merged, opts.dataType, flagRaw)
	}

	// Generate and inject hints — but never into --raw output, which is
	// documented as the original API response with nothing added.
	if !flagRaw {
		hints := output.GenerateHints(simplified, opts.dataType, opts.operation, totalLimit, opts.from, opts.to, opts.sleepDetail)
		if truncated {
			hints = append(hints, fmt.Sprintf(
				"returned %d rows = --limit; more data exists — fetch the next page with --page-token %s, "+
					"or raise --limit / narrow --from/--to",
				totalLimit, remainingToken))
		}
		simplified = output.InjectHints(simplified, hints)
		simplified = output.WithAPIVersion(output.EnsureEnvelope(simplified), activeAPIVersion)
	}

	return printOutput(simplified)
}

// executeDataGet fetches a single data point and reuses the list simplifier by
// wrapping the point in a one-element dataPoints array.
func executeDataGet(req *client.Request, opts dataListOpts) error {
	c := newClient()
	resp, err := c.Do(req)
	if err != nil {
		if cliErr, ok := err.(*client.CLIError); ok {
			return cliErr
		}
		return client.NewAPIError(0, err.Error(), "")
	}

	merged, _ := json.Marshal(map[string]interface{}{
		"dataPoints": []json.RawMessage{resp.Body},
	})

	var simplified json.RawMessage
	if opts.dataType == "sleep" {
		simplified = output.SimplifySleepResponse(merged, opts.sleepDetail, flagRaw)
	} else {
		simplified = output.SimplifyResponse(merged, opts.dataType, flagRaw)
	}

	if !flagRaw {
		hints := output.GenerateHints(simplified, opts.dataType, opts.operation, 0, "", "", opts.sleepDetail)
		simplified = output.InjectHints(simplified, hints)
		simplified = output.WithAPIVersion(output.EnsureEnvelope(simplified), activeAPIVersion)
	}

	return printOutput(simplified)
}

// executeDataRollup sends a rollup request, follows nextPageToken pagination
// (rollUp paginates via POST-with-body pageToken), simplifies, and injects hints.
func executeDataRollup(req *client.Request, opts dataListOpts) error {
	c := newClient()
	resp, err := c.Do(req)
	if err != nil {
		if cliErr, ok := err.(*client.CLIError); ok {
			return cliErr
		}
		return client.NewAPIError(0, err.Error(), "")
	}

	body := resp.Body
	// Only POST rollup endpoints paginate with a body pageToken; reconcile is
	// a GET and returns dataPoints, which collectRollupPages passes through.
	if req.Method == "POST" && len(req.Body) > 0 {
		baseBody := req.Body
		body, err = collectRollupPages(resp.Body, listPageCap, func(token string) (json.RawMessage, error) {
			pageBody, err := withPageToken(baseBody, token)
			if err != nil {
				return nil, client.NewAPIError(0,
					fmt.Sprintf("failed to build pagination request: %v", err), "")
			}
			req.Body = pageBody
			pageResp, err := c.Do(req)
			if err != nil {
				if cliErr, ok := err.(*client.CLIError); ok {
					return nil, cliErr
				}
				return nil, client.NewAPIError(0, err.Error(), "")
			}
			return pageResp.Body, nil
		})
		req.Body = baseBody
		if err != nil {
			return err
		}
	}

	simplified := output.SimplifyResponse(body, opts.dataType, flagRaw)

	if !flagRaw {
		hints := output.GenerateHints(body, opts.dataType, opts.operation, 0, opts.from, opts.to, opts.sleepDetail)
		simplified = output.InjectHints(simplified, hints)
		simplified = output.WithAPIVersion(output.EnsureEnvelope(simplified), activeAPIVersion)
	}

	return printOutput(simplified)
}

// withPageToken returns a copy of a JSON request body with pageToken set, so
// follow-up rollup pages repeat the same range/window with the new token.
func withPageToken(body []byte, token string) ([]byte, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["pageToken"] = token
	return json.Marshal(m)
}

// collectRollupPages mirrors collectPages for rollup responses, which paginate
// by re-POSTing the body with a pageToken. It merges all rollupDataPoints into
// a single response. A first page without a nextPageToken (or any unrecognized
// shape, e.g. reconcile's dataPoints) passes through unchanged. If the pageCap
// safety net is hit with the server still offering data, the remaining token
// is preserved in the merged output and a warning is printed.
func collectRollupPages(first json.RawMessage, pageCap int, fetchPage func(token string) (json.RawMessage, error)) (json.RawMessage, error) {
	type rollupPage struct {
		RollupDataPoints []json.RawMessage `json:"rollupDataPoints"`
		NextPageToken    string            `json:"nextPageToken"`
	}
	var page rollupPage
	if err := json.Unmarshal(first, &page); err != nil || page.NextPageToken == "" {
		return first, nil
	}

	all := page.RollupDataPoints
	token := page.NextPageToken
	for n := 1; n < pageCap && token != ""; n++ {
		raw, err := fetchPage(token)
		if err != nil {
			return nil, err
		}
		var p rollupPage
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, client.NewAPIError(0,
				fmt.Sprintf("failed to parse API response: %v", err),
				"The server returned a response that could not be decoded.")
		}
		all = append(all, p.RollupDataPoints...)
		token = p.NextPageToken
	}

	merged := map[string]interface{}{"rollupDataPoints": all}
	if token != "" {
		merged["nextPageToken"] = token
		fmt.Fprintf(os.Stderr,
			"warning: stopped after %d pages with more data still available; "+
				"narrow --from/--to to continue from the returned nextPageToken\n",
			pageCap)
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, client.NewAPIError(0, fmt.Sprintf("failed to merge pages: %v", err), "")
	}
	return out, nil
}

// ─── list ────────────────────────────────────────────────────────

func newListCommand(dt *types.DataType) *cobra.Command {
	var (
		from, to, filter string
		limit            int
		pageToken        string
		detail           bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: fmt.Sprintf("List %s data points", dt.ID),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := buildFilter(dt, from, to, filter)
			if err != nil {
				return err
			}
			query := url.Values{}
			if f != "" {
				query.Set("filter", f)
			}
			req := &client.Request{
				Method: "GET",
				Path:   fmt.Sprintf("/users/me/dataTypes/%s/dataPoints", dt.ID),
				Query:  query,
			}
			if flagDryRun {
				return doDryRun(req)
			}
			return executeDataList(req, dataListOpts{
				dataType:    dt.ID,
				operation:   "list",
				limit:       limit,
				pageToken:   pageToken,
				from:        from,
				to:          to,
				sleepDetail: detail,
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Start date: YYYY-MM-DD, ISO 8601, 'today', 'yesterday'")
	cmd.Flags().StringVar(&to, "to", "", "End date (inclusive of the named day): YYYY-MM-DD, ISO 8601, 'today', 'yesterday'")
	cmd.Flags().StringVar(&filter, "filter", "", "Raw API filter (overrides --from/--to)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Max total results (default: 500)")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "Resume from a previous response's nextPageToken (fetches the next page losslessly)")
	if dt.ID == "sleep" {
		cmd.Flags().BoolVar(&detail, "detail", false, "Include per-stage time breakdown")
	}
	return cmd
}

// ─── get ─────────────────────────────────────────────────────────

func newGetCommand(dt *types.DataType) *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "get",
		Short: fmt.Sprintf("Get a single %s data point by ID", dt.ID),
		Long: fmt.Sprintf(`Retrieve one %s data point by its ID.

Use 'ghealth data %s list --limit 5' to find data point IDs.`, dt.ID, dt.ID),
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return client.NewValidationError("--id is required",
					fmt.Sprintf("Use 'ghealth data %s list --limit 5' to find data point IDs", dt.ID))
			}
			req := &client.Request{
				Method: "GET",
				Path:   fmt.Sprintf("/users/me/dataTypes/%s/dataPoints/%s", dt.ID, id),
			}
			if flagDryRun {
				return doDryRun(req)
			}
			return executeDataGet(req, dataListOpts{
				dataType:  dt.ID,
				operation: "get",
			})
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Data point ID (required)")
	return cmd
}

// ─── create ──────────────────────────────────────────────────────

func newCreateCommand(dt *types.DataType) *cobra.Command {
	var jsonBody string
	cmd := &cobra.Command{
		Use:   "create",
		Short: fmt.Sprintf("Create %s data point", dt.ID),
		Long: fmt.Sprintf(`Create a new %s data point. Pass the request body as JSON via --json.

Use 'ghealth data %s list --raw --limit 1' to see the API's response format,
then model your create payload on the same structure.

The API returns an Operation object (write operations are asynchronous).`, dt.ID, dt.ID),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonBody == "" {
				return client.NewValidationError("--json is required",
					fmt.Sprintf("Use 'ghealth data %s list --raw --limit 1' to see the expected format", dt.ID))
			}
			req := &client.Request{
				Method: "POST",
				Path:   fmt.Sprintf("/users/me/dataTypes/%s/dataPoints", dt.ID),
				Body:   []byte(jsonBody),
			}
			if flagDryRun {
				return doDryRun(req)
			}
			return doRequest(req)
		},
	}
	cmd.Flags().StringVar(&jsonBody, "json", "", "Request body as JSON (required)")
	return cmd
}

// ─── update ──────────────────────────────────────────────────────

func newUpdateCommand(dt *types.DataType) *cobra.Command {
	var (
		id         string
		jsonBody   string
		updateMask string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: fmt.Sprintf("Update %s data point", dt.ID),
		Long: fmt.Sprintf(`Update an existing %s data point by ID.

Use 'ghealth data %s list --limit 5' to find data point IDs.
Use --update-mask to specify which fields to update (comma-separated).

The API returns an Operation object (write operations are asynchronous).`, dt.ID, dt.ID),
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return client.NewValidationError("--id is required",
					fmt.Sprintf("Use 'ghealth data %s list --limit 5' to find data point IDs", dt.ID))
			}
			if jsonBody == "" {
				return client.NewValidationError("--json is required", "Provide the fields to update as JSON")
			}
			req := &client.Request{
				Method: "PATCH",
				Path:   fmt.Sprintf("/users/me/dataTypes/%s/dataPoints/%s", dt.ID, id),
				Body:   []byte(jsonBody),
			}
			if updateMask != "" {
				req.Query = url.Values{"updateMask": {updateMask}}
			}
			if flagDryRun {
				return doDryRun(req)
			}
			return doRequest(req)
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Data point ID (required)")
	cmd.Flags().StringVar(&jsonBody, "json", "", "Fields to update as JSON (required)")
	cmd.Flags().StringVar(&updateMask, "update-mask", "", "Comma-separated field paths to update")
	return cmd
}

// ─── delete ──────────────────────────────────────────────────────

func newDeleteCommand(dt *types.DataType) *cobra.Command {
	var ids string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: fmt.Sprintf("Delete %s data points", dt.ID),
		Long: fmt.Sprintf(`Delete one or more %s data points by ID.

Use 'ghealth data %s list --limit 10' to find data point IDs.
Accepts up to 10,000 IDs per request.

The API returns an Operation object (write operations are asynchronous).`, dt.ID, dt.ID),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ids == "" {
				return client.NewValidationError("--ids is required",
					fmt.Sprintf("Use 'ghealth data %s list --limit 10' to find data point IDs", dt.ID))
			}
			idList := strings.Split(ids, ",")
			// Build full resource names as the API expects.
			names := make([]string, len(idList))
			for i, rawID := range idList {
				rawID = strings.TrimSpace(rawID)
				if strings.HasPrefix(rawID, "users/") {
					names[i] = rawID // Already a full resource name.
				} else {
					names[i] = fmt.Sprintf("users/me/dataTypes/%s/dataPoints/%s", dt.ID, rawID)
				}
			}
			body, _ := json.Marshal(map[string][]string{
				"names": names,
			})
			req := &client.Request{
				Method: "POST",
				Path:   fmt.Sprintf("/users/me/dataTypes/%s/dataPoints:batchDelete", dt.ID),
				Body:   body,
			}
			if flagDryRun {
				return doDryRun(req)
			}
			return doRequest(req)
		},
	}
	cmd.Flags().StringVar(&ids, "ids", "", "Comma-separated data point IDs (required)")
	return cmd
}

// ─── rollup ──────────────────────────────────────────────────────

// checkRollupRangeCap rejects ranges longer than the API allows per request:
// 14 days for the short-range set (heart-rate, total-calories, active-minutes,
// calories-in-heart-rate-zone), 90 days for other rollup-capable types.
func checkRollupRangeCap(dt *types.DataType, days float64) error {
	capDays := dt.RollupRangeCapDays()
	if days <= float64(capDays) {
		return nil
	}
	return client.NewValidationError(
		fmt.Sprintf("requested range is %.0f days; the API caps %s rollups at %d days per request", days, dt.ID, capDays),
		fmt.Sprintf("Split the range into chunks of at most %d days and merge the results", capDays),
	)
}

// validateRollupRange enforces the per-type range cap on a rollUp physical
// range before the request is sent, so the failure is a clear local
// validation error rather than an API 400.
func validateRollupRange(dt *types.DataType, from, to string) error {
	startStr, err := parsePhysicalTime(from)
	if err != nil {
		return err
	}
	endStr, err := parsePhysicalEndTime(to)
	if err != nil {
		return err
	}
	start, err1 := time.Parse(time.RFC3339, startStr)
	end, err2 := time.Parse(time.RFC3339, endStr)
	if err1 != nil || err2 != nil {
		return nil // unrecognized shape — let the API report it
	}
	return checkRollupRangeCap(dt, end.Sub(start).Hours()/24)
}

// validateDailyRollupRange enforces the same per-type cap on a dailyRollUp
// civil date range.
func validateDailyRollupRange(dt *types.DataType, from, to string) error {
	start, ok, err := civilDay(from)
	if err != nil || !ok {
		return err
	}
	var end time.Time
	if d, isBare := bareDate(to); isBare {
		end = d.AddDate(0, 0, 1) // --to is inclusive of the named day
	} else {
		end, ok, err = civilDay(to)
		if err != nil || !ok {
			return err
		}
	}
	return checkRollupRangeCap(dt, end.Sub(start).Hours()/24)
}

// civilDay resolves a date input to its civil day (at midnight UTC, for
// arithmetic only). ok is false when the shape is unrecognized.
func civilDay(s string) (time.Time, bool, error) {
	dateStr, err := parseDate(s)
	if err != nil {
		return time.Time{}, false, err
	}
	t, perr := time.Parse("2006-01-02", dateStr[:10])
	if perr != nil {
		return time.Time{}, false, nil
	}
	return t, true, nil
}

// buildRollupBody constructs the rollUp request body. rollUp's range is a
// physical-time Interval (startTime/endTime, RFC-3339, closed-open) plus a
// required windowSize duration — NOT a civil date range (that is dailyRollUp).
// A bare --to date is advanced one day so the range includes the named day.
func buildRollupBody(from, to, windowSize string) ([]byte, error) {
	startTime, err := parsePhysicalTime(from)
	if err != nil {
		return nil, err
	}
	endTime, err := parsePhysicalEndTime(to)
	if err != nil {
		return nil, err
	}
	rollupReq := map[string]interface{}{
		"range": map[string]interface{}{
			"startTime": startTime,
			"endTime":   endTime,
		},
		"windowSize": windowSize,
	}
	return json.Marshal(rollupReq)
}

func newRollupCommand(dt *types.DataType) *cobra.Command {
	var (
		from, to   string
		windowSize string
		jsonBody   string
	)
	cmd := &cobra.Command{
		Use:   "rollup",
		Short: fmt.Sprintf("Aggregate %s over a date range", dt.ID),
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" || to == "" {
				return client.NewValidationError("--from and --to are required", "")
			}
			var body []byte
			if jsonBody != "" {
				body = []byte(jsonBody)
			} else {
				if err := validateRollupRange(dt, from, to); err != nil {
					return err
				}
				var err error
				body, err = buildRollupBody(from, to, windowSize)
				if err != nil {
					return err
				}
			}
			req := &client.Request{
				Method: "POST",
				Path:   fmt.Sprintf("/users/me/dataTypes/%s/dataPoints:rollUp", dt.ID),
				Body:   body,
			}
			if flagDryRun {
				return doDryRun(req)
			}
			return executeDataRollup(req, dataListOpts{
				dataType: dt.ID, operation: "rollup", from: from, to: to,
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Start time (required): YYYY-MM-DD, RFC-3339, 'today', 'yesterday' (bare dates = local midnight)")
	cmd.Flags().StringVar(&to, "to", "", "End time (required, inclusive of the named day): YYYY-MM-DD, RFC-3339, 'today', 'yesterday'")
	cmd.Flags().StringVar(&windowSize, "window-size", "86400s", "Aggregation window as a duration (e.g. 3600s, 86400s)")
	cmd.Flags().StringVar(&jsonBody, "json", "", "Override request body")
	return cmd
}

// ─── daily-rollup ────────────────────────────────────────────────

// buildDailyRollupBody constructs the dailyRollUp request body. The range is
// a CivilTimeInterval (closed-open); a bare --to date is advanced one day so
// the range includes the named day. windowSizeDays is documented as optional
// (default 1), but the live API (revision 20260528) returns HTTP 400 when it
// is omitted, so always send it explicitly.
func buildDailyRollupBody(from, to string, windowDays int) ([]byte, error) {
	startCivil, err := parseCivilDate(from)
	if err != nil {
		return nil, err
	}
	endCivil, err := parseCivilEndDate(to)
	if err != nil {
		return nil, err
	}
	rollupReq := map[string]interface{}{
		"range": map[string]interface{}{
			"start": startCivil,
			"end":   endCivil,
		},
		"windowSizeDays": windowDays,
	}
	return json.Marshal(rollupReq)
}

func newDailyRollupCommand(dt *types.DataType) *cobra.Command {
	var (
		from, to   string
		jsonBody   string
		windowDays int
	)
	cmd := &cobra.Command{
		Use:   "daily-rollup",
		Short: fmt.Sprintf("Daily totals for %s over a date range", dt.ID),
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" || to == "" {
				return client.NewValidationError("--from and --to are required", "")
			}
			if windowDays < 1 {
				return client.NewValidationError("--window-days must be a positive integer", "")
			}
			var body []byte
			if jsonBody != "" {
				body = []byte(jsonBody)
			} else {
				if err := validateDailyRollupRange(dt, from, to); err != nil {
					return err
				}
				var err error
				body, err = buildDailyRollupBody(from, to, windowDays)
				if err != nil {
					return err
				}
			}
			req := &client.Request{
				Method: "POST",
				Path:   fmt.Sprintf("/users/me/dataTypes/%s/dataPoints:dailyRollUp", dt.ID),
				Body:   body,
			}
			if flagDryRun {
				return doDryRun(req)
			}
			return executeDataRollup(req, dataListOpts{
				dataType: dt.ID, operation: "daily-rollup", from: from, to: to,
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Start date (required): YYYY-MM-DD, 'today', 'yesterday'")
	cmd.Flags().StringVar(&to, "to", "", "End date (required, inclusive of the named day): YYYY-MM-DD, 'today', 'yesterday'")
	cmd.Flags().IntVar(&windowDays, "window-days", 1, "Aggregation window size in days")
	cmd.Flags().StringVar(&jsonBody, "json", "", "Override request body")
	return cmd
}

// ─── reconcile ───────────────────────────────────────────────────

func newReconcileCommand(dt *types.DataType) *cobra.Command {
	var from, to, filter string
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: fmt.Sprintf("Reconcile %s data", dt.ID),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := buildFilter(dt, from, to, filter)
			if err != nil {
				return err
			}
			query := url.Values{}
			if f != "" {
				query.Set("filter", f)
			}
			req := &client.Request{
				Method: "GET",
				Path:   fmt.Sprintf("/users/me/dataTypes/%s/dataPoints:reconcile", dt.ID),
				Query:  query,
			}
			if flagDryRun {
				return doDryRun(req)
			}
			return executeDataRollup(req, dataListOpts{
				dataType: dt.ID, operation: "reconcile", from: from, to: to,
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Start date")
	cmd.Flags().StringVar(&to, "to", "", "End date")
	cmd.Flags().StringVar(&filter, "filter", "", "Raw API filter (overrides --from/--to)")
	return cmd
}

// ─── export-tcx ──────────────────────────────────────────────────

func newExportTCXCommand(dt *types.DataType) *cobra.Command {
	var (
		id         string
		outputFile string
		asFormat   string
	)
	cmd := &cobra.Command{
		Use:   "export-tcx",
		Short: fmt.Sprintf("Export %s as TCX, or as a trackpoint CSV with --as csv", dt.ID),
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return client.NewValidationError("--id is required", "Provide the data point ID")
			}
			if outputFile == "" {
				return client.NewValidationError("--output is required", "Provide the output file path, or '-' for stdout")
			}
			if asFormat != "tcx" && asFormat != "csv" {
				return client.NewValidationError(
					fmt.Sprintf("invalid --as value: %s", asFormat),
					"Use --as tcx (raw export) or --as csv (one row per trackpoint)",
				)
			}
			req := &client.Request{
				Method: "GET",
				Path:   fmt.Sprintf("/users/me/dataTypes/%s/dataPoints/%s:exportExerciseTcx", dt.ID, id),
				// HTTP clients must request alt=media to receive the raw TCX file.
				// Without it the API returns a JSON ExportExerciseTcxResponse
				// envelope ({"tcxData": ...}), which would be written verbatim to
				// the .tcx file instead of valid TCX/XML.
				Query: url.Values{"alt": {"media"}},
			}
			if flagDryRun {
				return doDryRun(req)
			}
			c := newClient()
			resp, err := c.Do(req)
			if err != nil {
				if cliErr, ok := err.(*client.CLIError); ok {
					return cliErr
				}
				return client.NewAPIError(0, err.Error(), "")
			}
			payload := resp.Body
			rows := -1
			if asFormat == "csv" {
				var buf bytes.Buffer
				rows, err = output.WriteTCXAsCSV(resp.Body, &buf)
				if err != nil {
					return client.NewValidationError(
						fmt.Sprintf("convert TCX to CSV: %v", err),
						"Re-run with --as tcx to inspect the raw export",
					)
				}
				payload = buf.Bytes()
			}
			if outputFile == "-" {
				if _, err := os.Stdout.Write(payload); err != nil {
					return client.NewValidationError(fmt.Sprintf("write to stdout: %v", err), "")
				}
			} else {
				if err := os.WriteFile(outputFile, payload, 0644); err != nil {
					return client.NewValidationError(
						fmt.Sprintf("failed to write file: %v", err),
						"Check that the output directory exists and is writable",
					)
				}
				if rows >= 0 {
					// Schema peek: row count + header + first rows, so an agent
					// learns the CSV shape without opening the file.
					fmt.Fprintf(os.Stderr, "Exported %d trackpoint row(s) to %s\n%s", rows, outputFile, csvPeek(payload, 3))
					if rows == 0 {
						fmt.Fprintln(os.Stderr, "0 rows = no time-series track (indoor/no-sensor activity); session summary and notes are in 'data exercise list'.")
					}
				} else {
					fmt.Fprintf(os.Stderr, "Exported to %s\n", outputFile)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Data point ID (required)")
	cmd.Flags().StringVar(&outputFile, "output", "", "Output file path (required; '-' writes to stdout)")
	cmd.Flags().StringVar(&asFormat, "as", "tcx", "Output format: tcx (raw Google export) or csv (one row per trackpoint: time, lap, position, altitude, distance, heart rate; lap-less indoor activities yield a header-only CSV)")
	return cmd
}

// csvPeek returns the header plus up to n data rows of a CSV payload, indented
// for stderr display.
func csvPeek(payload []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(payload), "\n"), "\n")
	if len(lines) > n+1 {
		lines = lines[:n+1]
	}
	return "  " + strings.Join(lines, "\n  ") + "\n"
}
