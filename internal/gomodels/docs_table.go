// Package gomodels derives the OpenCode Go model list and each model's wire
// format from OpenCode's own published sources, so new models need no config
// edits.
package gomodels

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/routatic/proxy/internal/core"
)

// DocsModel is one row of the "Endpoints" table in OpenCode's Go docs
// (packages/web/src/content/docs/go.mdx in the opencode repository).
type DocsModel struct {
	ID         string
	Name       string
	WireFormat core.WireFormat
}

const endpointsHeading = "## Endpoints"

// endpointWireFormats maps the path after /v1/ to the wire format it serves.
var endpointWireFormats = map[string]core.WireFormat{
	"chat/completions": core.WireFormatOpenAIChat,
	"messages":         core.WireFormatAnthropic,
	"responses":        core.WireFormatOpenAIResponses,
}

// ParseDocsTable reads the Endpoints table from the Go docs markdown.
//
// It fails rather than returning a partial list when the table is missing,
// its columns are renamed, or a row names an endpoint it does not recognise,
// so a docs format change surfaces as an error instead of silently dropping
// models.
func ParseDocsTable(markdown string) ([]DocsModel, error) {
	scanner := bufio.NewScanner(strings.NewReader(markdown))
	inSection := false
	var idCol, nameCol, endpointCol = -1, -1, -1
	var models []DocsModel

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !inSection {
			inSection = line == endpointsHeading
			continue
		}
		if strings.HasPrefix(line, "## ") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			if len(models) > 0 {
				break
			}
			continue
		}

		cells := splitRow(line)
		if idCol < 0 {
			for i, cell := range cells {
				switch cell {
				case "Model ID":
					idCol = i
				case "Model":
					nameCol = i
				case "Endpoint":
					endpointCol = i
				}
			}
			if idCol < 0 || nameCol < 0 || endpointCol < 0 {
				return nil, fmt.Errorf("endpoints table header %q lacks Model, Model ID or Endpoint column", line)
			}
			continue
		}
		if isSeparatorRow(cells) {
			continue
		}
		if len(cells) <= max(idCol, nameCol, endpointCol) {
			return nil, fmt.Errorf("endpoints table row %q has too few columns", line)
		}

		endpoint := cells[endpointCol]
		_, path, found := strings.Cut(endpoint, "/v1/")
		wireFormat, known := endpointWireFormats[path]
		if !found || !known {
			return nil, fmt.Errorf("model %q uses unrecognised endpoint %q", cells[idCol], endpoint)
		}
		models = append(models, DocsModel{ID: cells[idCol], Name: cells[nameCol], WireFormat: wireFormat})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read docs markdown: %w", err)
	}

	switch {
	case !inSection:
		return nil, fmt.Errorf("docs markdown has no %q section", endpointsHeading)
	case idCol < 0:
		return nil, fmt.Errorf("%q section has no table", endpointsHeading)
	case len(models) == 0:
		return nil, fmt.Errorf("%q table has no model rows", endpointsHeading)
	}
	return models, nil
}

// splitRow splits a markdown table row into trimmed cells, dropping code
// backticks.
func splitRow(line string) []string {
	parts := strings.Split(strings.Trim(line, "|"), "|")
	cells := make([]string, len(parts))
	for i, part := range parts {
		cells[i] = strings.Trim(strings.TrimSpace(part), "`")
	}
	return cells
}

func isSeparatorRow(cells []string) bool {
	for _, cell := range cells {
		if strings.Trim(cell, "-: ") != "" {
			return false
		}
	}
	return true
}
