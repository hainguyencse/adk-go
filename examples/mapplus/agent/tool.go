package agent

import (
	"fmt"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ================= Tool search_location ======================
func newSearchLocationTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "search_location",
			Description: "Search location for MAP+",
		},
		searchLocation,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create search_location tool: %w", err)
	}

	return funcTool, nil
}

type searchLocationInput struct {
	PropertyType string `json:"propertyType,omitempty" description:"the property type"`
	LocationType string `json:"locationType" description:"the location type"`
	Keyword      string `json:"keyword,omitempty" description:"search keyword location"`
	Radius       string `json:"radius,omitempty" description:"search radius"`
}

type searchLocationOutput struct {
	PropertyType string `json:"propertyType"`
	LocationType string `json:"locationType"`
	LocationIDs  string `json:"locationIDs"`
	Radius       string `json:"radius"`
}

func searchLocation(ctx tool.Context, input searchLocationInput) (searchLocationOutput, error) {
	radius := input.Radius
	if radius == "" {
		radius = "1000"
	}

	return searchLocationOutput{
		PropertyType: input.PropertyType,
		LocationType: input.LocationType,
		LocationIDs:  input.Keyword,
		Radius:       radius,
	}, nil
}

// ================= Tool analytics_location ======================
func newAnalyticsLocationTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "analytics_location",
			Description: "Analytics Location",
		},
		analyticsLocation,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	return funcTool, nil
}

type analyticsLocationInput struct {
	// Output from search_agent (tool: search_location)
	PropertyType string `json:"propertyType" description:"propertyType from search"`
	LocationType string `json:"locationType" description:"locationType from search"`
	LocationIDs  string `json:"locationIDs"  description:"locationIDs from search"`
	Radius       string `json:"radius"       description:"radius from search as a string (e.g. '1000', '2000'). always send as a quoted string, never as a number"`
}

type analyticsLocationOutput struct {
	ProjectIDs string `json:"projectIds"`
}

func analyticsLocation(ctx tool.Context, input analyticsLocationInput) (analyticsLocationOutput, error) {
	return analyticsLocationOutput{
		ProjectIDs: "100,200,300",
	}, nil
}

// ================= Tool summary_location ======================
func newSummaryLocationTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "summary_location",
			Description: "Summary Location",
		},
		summaryLocation,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	return funcTool, nil
}

type summaryLocationInput struct {
	// Output from search_agent (tool: analytics_location)
	ProjectID string `json:"projectId" description:"the project ID as a string (e.g. '100', '200'). always send as a quoted string, never as a number"`
	Action    string `json:"action" description:"action to perform on the project. supported values: export_pdf, export_image"`
}

type summaryLocationOutput struct {
	Action    string `json:"action"`
	Message   string `json:"message"`
	ProjectID string `json:"projectId"`
}

func summaryLocation(ctx tool.Context, input summaryLocationInput) (summaryLocationOutput, error) {
	return summaryLocationOutput{
		Action:    input.Action,
		ProjectID: input.ProjectID,
		Message:   fmt.Sprintf("[%s]: Project: %s", input.Action, input.ProjectID),
	}, nil
}
