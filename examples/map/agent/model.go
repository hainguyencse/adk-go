package agent

import (
	"fmt"
	"strings"
)

const (
	locationTypeMRT           = "mrt_station"
	locationTypeDistrict      = "district"
	locationTypeEstate        = "estate"
	locationTypePrimarySchool = "primary_school"
)

const (
	defaultSearchLocationRadius = 1000 // meters

	SessionStateKeyMapRequest  = "map_request"
	SessionStateKeyMapResponse = "map_response"
)

type searchLocationInput struct {
	LocationType string `json:"locationType" description:"the location type"`
	Keyword      string `json:"keyword" description:"search keyword location"`
	Radius       string `json:"radius" description:"search radius"`
}

type searchLocationOutput struct {
	LocationType string `json:"locationType"`
	Keyword      string `json:"keyword"`
	Radius       string `json:"radius"`
}

const (
	filterTypeNumberOfBedrooms = "numberOfBedrooms"
	filterTypeNewLaunchProject = "newLaunch"
	filterTransactionDateRange = "transactionDateRange"
)

type filterProjectInput struct {
	FilterType  string `json:"filterType"`
	FilterValue string `json:"filterValue"`
}

type filterProjectOutput struct {
	FilterType  string `json:"filterType"`
	FilterValue string `json:"filterValue"`
}

// For zoom
type zoomMapInput struct {
	ZoomLevel string `json:"zoomLevel" description:"zoom level"`
}

type zoomMapOutput struct {
	ZoomLevel string `json:"zoomLevel"`
}

// For export pdf
type exportPDFInput struct {
}

type exportPDFOutput struct {
}

// Update Map
type updateMapInput struct{}

type updateMapOutput struct {
	Count string `json:"count"`
}

// mapQueryInput is the unified input for execute_map_query tool.
// All fields are optional — only include what the user mentioned.
type mapQueryInput struct {
	LocationType         string `json:"locationType,omitempty" description:"mrt_station, district, estate, or primary_school"`
	Keyword              string `json:"keyword,omitempty" description:"location name to search nearby"`
	Radius               string `json:"radius,omitempty" description:"search radius in meters, 1000 to 4000"`
	NumberOfBedrooms     string `json:"numberOfBedrooms,omitempty" description:"1, 2, 3, 4, or 5"`
	IsNewLaunch          string `json:"isNewLaunch,omitempty" description:"newLaunch to show only new launches"`
	TransactionDateRange string `json:"transactionDateRange,omitempty" description:"1y, 3y, 5y, or 10y"`
}

type MapQueryOutput struct {
	LocationIDs          string `json:"locationIDs"`
	LocationType         string `json:"locationType"`
	Keyword              string `json:"keyword"`
	Radius               string `json:"radius"`
	IsNewLaunch          string `json:"isNewLaunch"`
	UnitBedroomTypes     string `json:"unitBedroomTypes"`
	TransactionDateRange string `json:"transactionDateRange"`
	ZoomLevel            string `json:"zoomLevel"`
}

// MapRequest
type MapRequest struct {
	LocationIDs          []string `json:"locationIDs"`
	LocationType         string   `json:"locationType"`
	Keyword              string   `json:"keyword"`
	Radius               int      `json:"radius"`
	IsNewLaunch          bool     `json:"isNewLaunch"`
	UnitBedroomTypes     []string `json:"unitBedroomTypes"`
	TransactionDateRange string   `json:"transactionDateRange"`
	ZoomLevel            int      `json:"zoomLevel"`
}

func (mr *MapRequest) ToMapQueryOutput() MapQueryOutput {
	output := MapQueryOutput{
		LocationType:         mr.LocationType,
		Keyword:              mr.Keyword,
		Radius:               fmt.Sprintf("%d", mr.Radius),
		UnitBedroomTypes:     strings.Join(mr.UnitBedroomTypes, ","),
		TransactionDateRange: mr.TransactionDateRange,
		ZoomLevel:            fmt.Sprintf("%d", mr.ZoomLevel),
	}

	if len(mr.LocationIDs) > 0 {
		output.LocationIDs = strings.Join(mr.LocationIDs, ",")
	}

	if mr.IsNewLaunch {
		output.IsNewLaunch = "newLaunch"
	}

	return output
}

type MapItem struct {
	Location struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		PropertyType string `json:"propertyType"`
		District     string `json:"district"`
	} `json:"location"`
}

type MapResponse struct {
	Data []MapItem `json:"data"`
}
