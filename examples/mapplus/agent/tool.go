package agent

import (
	"fmt"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

const restartSequenceStateKey = "restart_sequence"

// ================= Tool task_completed ======================
type taskCompletedInput struct{}

type taskCompletedOutput struct {
	Result string `json:"result"`
}

func newTaskCompletedTool() (tool.Tool, error) {
	return functiontool.New(
		functiontool.Config{
			Name:        "task_completed",
			Description: "Signals that this agent has finished its task. Call this when done so the next agent can run.",
		},
		func(ctx tool.Context, args taskCompletedInput) (taskCompletedOutput, error) {
			ctx.Actions().Escalate = true
			return taskCompletedOutput{Result: "Task completed."}, nil
		},
	)
}

// ================= Tool restart_sequence ======================
type restartSequenceInput struct{}

type restartSequenceOutput struct {
	Result string `json:"result"`
}

func newRestartSequenceTool() (tool.Tool, error) {
	return functiontool.New(
		functiontool.Config{
			Name:        "restart_sequence",
			Description: "Restarts the whole pipeline from search_agent when the user wants to change their search requirements.",
		},
		func(ctx tool.Context, args restartSequenceInput) (restartSequenceOutput, error) {
			ctx.Actions().Escalate = true
			if ctx.Actions().StateDelta == nil {
				ctx.Actions().StateDelta = make(map[string]any)
			}
			ctx.Actions().StateDelta[restartSequenceStateKey] = true
			return restartSequenceOutput{Result: "Restarting sequence from search."}, nil
		},
	)
}

// ================= Tool search_location ======================
type searchLocationInput struct {
	// Search nearby
	LocationType string `json:"locationType" description:"the location type"`
	Keyword      string `json:"keyword" description:"search keyword location"`
	Radius       string `json:"radius,omitempty" description:"search radius"`

	// Filter
	PropertyType string `json:"propertyType" description:"the property type"`

	ClientType string `json:"clientType" description:"user client type. options: buyer/seller/landlord/tenant"`
}

type searchLocationOutput struct {
	LocationType string `json:"locationType"`
	LocationIDs  string `json:"locationIDs"`
	Radius       string `json:"radius"`

	PropertyType string `json:"propertyType"`
	ClientType   string `json:"clientType"`
}

func newSearchLocationTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "search_location",
			Description: "Search location for MAP+",
		},
		func(ctx tool.Context, input searchLocationInput) (searchLocationOutput, error) {
			radius := input.Radius
			if radius == "" {
				radius = "1000"
			}

			result := searchLocationOutput{
				PropertyType: input.PropertyType,
				LocationType: input.LocationType,
				LocationIDs:  input.Keyword,
				Radius:       radius,
				ClientType:   input.ClientType,
			}

			ctx.State().Set("search_result", map[string]any{
				"propertyType": result.PropertyType,
				"locationType": result.LocationType,
				"locationIDs":  result.LocationIDs,
				"radius":       result.Radius,
				"clientType":   result.ClientType,
			})

			return result, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create search_location tool: %w", err)
	}

	return funcTool, nil
}

// ================= Tool analytics_location ======================
type analyticsLocationInput struct {
	// Output from search_agent (tool: search_location)
	LocationType string `json:"locationType" description:"locationType from search"`
	LocationIDs  string `json:"locationIDs"  description:"locationIDs from search"`
	Radius       string `json:"radius"       description:"radius from search as a string (e.g. '1000', '2000'). always send as a quoted string, never as a number"`

	PropertyType string `json:"propertyType" description:"propertyType from search"`
	ClientType   string `json:"clientType" description:"clientType from search"`

	// New Input
	UserGoal string `json:"userGoal" description:"user goal"`
}

type analyticsLocationOutput struct {
	LocationType string `json:"locationType"`
	LocationIDs  string `json:"locationIDs"`
	Radius       string `json:"radius"`

	PropertyType string `json:"propertyType"`
	ClientType   string `json:"clientType"`
	UserGoal     string `json:"userGoal"`

	SortBy               string `json:"sortBy"`
	SortOrder            string `json:"sortOrder"`
	SuggestionProjectIDs string `json:"suggestionProjectIds"`
}

func newAnalyticsLocationTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "analytics_location",
			Description: "Analytics Location",
		},
		func(ctx tool.Context, input analyticsLocationInput) (analyticsLocationOutput, error) {
			sortBy, sortOrder := userGoalToSortMetrics(input.UserGoal)

			result := analyticsLocationOutput{
				LocationType: input.LocationType,
				LocationIDs:  input.LocationIDs,
				Radius:       input.Radius,
				PropertyType: input.PropertyType,
				ClientType:   input.ClientType,
				UserGoal:     input.UserGoal,
				SortBy:       sortBy,
				SortOrder:    sortOrder,

				SuggestionProjectIDs: "100,200,300",
			}

			ctx.State().Set("analytics_result", map[string]any{
				"propertyType":         result.PropertyType,
				"locationType":         result.LocationType,
				"locationIDs":          result.LocationIDs,
				"radius":               result.Radius,
				"clientType":           result.ClientType,
				"userGoal":             result.UserGoal,
				"sortBy":               result.SortBy,
				"sortOrder":            result.SortOrder,
				"suggestionProjectIds": result.SuggestionProjectIDs,
			})

			return result, nil
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	return funcTool, nil
}

// ================= Tool summary_location ======================

type summaryLocationInput struct {
	// Output from analytics_agent (tool: analytics_location)
	LocationType         string `json:"locationType" description:"locationType from analytics_agent"`
	LocationIDs          string `json:"locationIDs" description:"locationIDs from analytics_agent"`
	Radius               string `json:"radius" description:"radius from analytics_agent"`
	PropertyType         string `json:"propertyType" description:"propertyType from analytics_agent"`
	UserGoal             string `json:"userGoal" description:"userGoal from analytics_agent"`
	SortBy               string `json:"sortBy" description:"sortBy from analytics_agent"`
	SortOrder            string `json:"sortOrder" description:"sortOrder from analytics_agent"`
	SuggestionProjectIDs string `json:"suggestionProjectIds" description:"suggestionProjectIds from analytics_agent"`

	// New Input
	ProjectID string `json:"projectId" description:"the project ID as a string (e.g. '100', '200'). always send as a quoted string, never as a number"`
	Action    string `json:"action" description:"action to perform on the project. supported values: export_pdf, export_image"`
}

type summaryLocationOutput struct {
	PropertyType         string `json:"propertyType"`
	LocationType         string `json:"locationType"`
	LocationIDs          string `json:"locationIDs"`
	Radius               string `json:"radius"`
	UserGoal             string `json:"userGoal"`
	SortBy               string `json:"sortBy"`
	SortOrder            string `json:"sortOrder"`
	SuggestionProjectIDs string `json:"suggestionProjectIds"`

	Action    string `json:"action"`
	ProjectID string `json:"projectId"`
	Message   string `json:"message"`
}

func newSummaryLocationTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "summary_location",
			Description: "Summary Location",
		},
		func(ctx tool.Context, input summaryLocationInput) (summaryLocationOutput, error) {
			return summaryLocationOutput{
				PropertyType: input.PropertyType,
				LocationType: input.LocationType,
				LocationIDs:  input.LocationIDs,
				Radius:       input.Radius,
				UserGoal:     input.UserGoal,
				SortBy:       input.SortBy,
				SortOrder:    input.UserGoal,
				Action:       input.Action,
				ProjectID:    input.ProjectID,
				Message:      fmt.Sprintf("[%s]: Project: %s", input.Action, input.ProjectID),
			}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	return funcTool, nil
}
