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
	LocationType string `json:"locationType" description:"the location type"`
	Keyword      string `json:"keyword" description:"search keyword location"`
	Radius       string `json:"radius" description:"search radius"`
}

type searchLocationOutput struct {
	LocationType   string `json:"locationType"`
	Keyword        string `json:"keyword"`
	Radius         string `json:"radius"`
	LocationResult string `json:"locationResult"`
}

func searchLocation(ctx tool.Context, input searchLocationInput) (searchLocationOutput, error) {
	return searchLocationOutput{
		LocationType:   input.LocationType,
		Keyword:        input.Keyword,
		Radius:         input.Radius,
		LocationResult: fmt.Sprintf("%s: %s", input.LocationType, input.Keyword),
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
	LocationType string `json:"locationType"   description:"location type from search"`
	Keyword      string `json:"keyword"        description:"keyword from search"`
	Radius       string `json:"radius"         description:"radius from search"`
	ProjectID    string `json:"projectId"  	   description:"project id that user select"`
}

type analyticsLocationOutput struct {
	ProjectID string `json:"projectId"`
}

func analyticsLocation(ctx tool.Context, input analyticsLocationInput) (analyticsLocationOutput, error) {
	fmt.Println("analyticsLocation input.LocationType", input.LocationType)
	fmt.Println("analyticsLocation input.Keyword", input.Keyword)
	fmt.Println("analyticsLocation input.Radius", input.Radius)

	return analyticsLocationOutput{
		ProjectID: input.ProjectID,
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
	ProjectID string `json:"projectId" description:"projectId from analytics agent"`
	Action    string `json:"action" description:"action from user. example: export pdf, export image"`
}

type summaryLocationOutput struct {
	ProjectID string `json:"projectId"`
	Action    string `json:"action"`
}

func summaryLocation(ctx tool.Context, input summaryLocationInput) (summaryLocationOutput, error) {
	fmt.Println("summaryLocation input.ProjectID", input.ProjectID)

	return summaryLocationOutput{
		ProjectID: input.ProjectID,
		Action:    fmt.Sprintf("Action %s for project: %s", input.Action, input.ProjectID),
	}, nil
}
