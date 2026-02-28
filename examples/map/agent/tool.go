package agent

import (
	"fmt"
	"log"
	"strconv"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

func newSearchLocationTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "search_location",
			Description: "Search location for MAP+. Can be nearby: MRTs/Primary School/Districts/Estates",
		},
		searchLocation,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create search_location tool: %w", err)
	}

	return funcTool, nil
}

func searchLocation(ctx tool.Context, input searchLocationInput) (searchLocationOutput, error) {
	fmt.Println("searchLocation run")

	locationIDs, err := searchLocationByKeyword(ctx, input.Keyword, input.LocationType)
	if err != nil {
		return searchLocationOutput{}, err
	}

	radius, err := strconv.Atoi(input.Radius)
	if err != nil || radius == 0 {
		radius = defaultSearchLocationRadius
	}

	var mapRequest MapRequest
	val, err := ctx.State().Get(sessionStateKeyMapRequest)
	if err == nil {
		mapRequest, _ = val.(MapRequest)
	}

	mapRequest.LocationIDs = locationIDs
	mapRequest.LocationType = input.LocationType
	mapRequest.Radius = radius

	ctx.State().Set(sessionStateKeyMapRequest, mapRequest)
	return searchLocationOutput{}, nil
}

func searchLocationByKeyword(ctx tool.Context, keyword string, locationType string) ([]string, error) {
	fmt.Println("searchLocationByKeyword run")

	switch locationType {
	case locationTypePrimarySchool:
		return searchPrimarySchoolByKeyword(keyword)
	}

	return []string{}, nil
}

func newFilterProjectTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "filter_project",
			Description: "Filter project from selected location for MAP+",
		},
		filterProject,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create filter_project tool: %w", err)
	}

	return funcTool, nil
}

func filterProject(ctx tool.Context, input filterProjectInput) (filterProjectOutput, error) {
	fmt.Println("filterProject run")

	var mapRequest MapRequest
	val, err := ctx.State().Get(sessionStateKeyMapRequest)
	if err == nil {
		mapRequest, _ = val.(MapRequest)
	}

	switch input.FilterType {
	case filterTypeNewLaunchProject:
		if input.FilterValue == "newLaunch" {
			mapRequest.IsNewLaunch = true
		} else {
			mapRequest.IsNewLaunch = false
		}
	case filterTypeNumberOfBedrooms:
		mapUnitBedroomTypes := map[string]string{
			"1": "1_bedroom",
			"2": "2_bedrooms",
			"3": "3_bedrooms",
			"4": "4_bedrooms",
			"5": "5_bedrooms",
		}

		if val, _ := mapUnitBedroomTypes[input.FilterValue]; val != "" {
			mapRequest.UnitBedroomTypes = []string{val}
		}
	case filterTransactionDateRange:
		mapDateRange := map[string]string{
			"1y":  "1y",
			"3y":  "3y",
			"5y":  "5y",
			"10y": "10y",
		}
		if val, _ := mapDateRange[input.FilterValue]; val != "" {
			mapRequest.TransactionDateRange = val
		}
	}

	ctx.State().Set(sessionStateKeyMapRequest, mapRequest)
	return filterProjectOutput{}, nil
}

func newUpdateMapTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "update_map",
			Description: "Update Map in MAP+",
		},
		updateMap,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create update_map tool: %w", err)
	}

	return funcTool, nil
}

func updateMap(ctx tool.Context, input updateMapInput) (updateMapOutput, error) {
	fmt.Println("updateMap run")

	var mapRequest MapRequest
	val, err := ctx.State().Get(sessionStateKeyMapRequest)
	if err == nil {
		mapRequest, _ = val.(MapRequest)
	}

	mapResponse, err := searchProjectsInMap(mapRequest)
	if err != nil {
		return updateMapOutput{}, nil
	}

	count := len(mapResponse.Data)
	return updateMapOutput{Count: fmt.Sprintf("%d", count)}, nil
}

func newZoomMapTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "zoom_map",
			Description: "Zoom Map in MAP+",
		},
		zoomMap,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create zoom_map tool: %w", err)
	}

	return funcTool, nil
}

func zoomMap(ctx tool.Context, input zoomMapInput) (zoomMapOutput, error) {
	var mapRequest MapRequest
	val, err := ctx.State().Get(sessionStateKeyMapRequest)
	if err == nil {
		mapRequest, _ = val.(MapRequest)
	}

	zoomLevel, _ := strconv.Atoi(input.ZoomLevel)

	if zoomLevel == 0 {
		zoomLevel = 10
	}

	mapRequest.ZoomLevel = zoomLevel
	ctx.State().Set(sessionStateKeyMapRequest, mapRequest)

	return zoomMapOutput{}, nil
}

func newExportPDFTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "export_pdf",
			Description: "Export PDF in MAP+",
		},
		exportPDF,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create export_pdf tool: %w", err)
	}

	return funcTool, nil
}

func exportPDF(ctx tool.Context, input exportPDFInput) (exportPDFOutput, error) {
	return exportPDFOutput{}, nil
}

func newExecuteMapQueryTool() (tool.Tool, error) {
	funcTool, err := functiontool.New(
		functiontool.Config{
			Name:        "execute_map_query",
			Description: "Search nearby locations and apply property filters in MAP+. Refreshes the map automatically. All parameters are optional — only include what the user mentioned.",
		},
		executeMapQuery,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create execute_map_query tool: %w", err)
	}

	return funcTool, nil
}

func executeMapQuery(ctx tool.Context, input mapQueryInput) (mapQueryOutput, error) {
	log.Printf("executeMapQuery run: %+v\n", input)

	var mapRequest MapRequest
	val, err := ctx.State().Get(sessionStateKeyMapRequest)
	if err == nil {
		mapRequest, _ = val.(MapRequest)
	}

	// Update location only if keyword is provided
	if input.Keyword != "" {
		locationIDs, _ := searchLocationByKeyword(ctx, input.Keyword, input.LocationType)
		mapRequest.LocationIDs = locationIDs
		mapRequest.LocationType = input.LocationType
	}

	// Update radius if provided
	if input.Radius != "" {
		radius, err := strconv.Atoi(input.Radius)
		if err != nil || radius < 1000 {
			radius = defaultSearchLocationRadius
		}
		if radius > 4000 {
			radius = 4000
		}
		mapRequest.Radius = radius
	}
	if mapRequest.Radius == 0 {
		mapRequest.Radius = defaultSearchLocationRadius
	}

	// Update bedroom filter if provided
	if input.NumberOfBedrooms != "" {
		mapUnitBedroomTypes := map[string]string{
			"1": "1_bedroom",
			"2": "2_bedrooms",
			"3": "3_bedrooms",
			"4": "4_bedrooms",
			"5": "5_bedrooms",
		}
		if v, ok := mapUnitBedroomTypes[input.NumberOfBedrooms]; ok {
			mapRequest.UnitBedroomTypes = []string{v}
		}
	}

	// Update new launch filter if provided
	if input.IsNewLaunch == "newLaunch" {
		mapRequest.IsNewLaunch = true
	}

	// Update transaction date range if provided
	if input.TransactionDateRange != "" {
		validRanges := map[string]bool{"1y": true, "3y": true, "5y": true, "10y": true}
		if validRanges[input.TransactionDateRange] {
			mapRequest.TransactionDateRange = input.TransactionDateRange
		}
	}

	ctx.State().Set(sessionStateKeyMapRequest, mapRequest)

	mapResponse, err := searchProjectsInMap(mapRequest)
	if err != nil {
		fmt.Printf("executeMapQuery error: %v\n", err)
		return mapQueryOutput{Count: "0"}, nil
	}

	count := len(mapResponse.Data)
	log.Printf("executeMapQuery found %d projects\n", count)
	return mapQueryOutput{Count: fmt.Sprintf("%d", count)}, nil
}
