package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	defaultDateRange = "3y"
)

func searchPrimarySchoolByKeyword(keyword string) ([]string, error) {
	// Call staging api for testing purpose
	// Should call func app directly
	baseUrl := os.Getenv("BASE_URL")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/places/schools/grouped-by-level", baseUrl), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Basic "+os.Getenv("API_TOKEN"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call schools API: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		Groups []struct {
			Level   string `json:"level"`
			Schools []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"schools"`
		} `json:"groups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var ids []string
	lowerKeyword := strings.ToLower(keyword)
	for _, group := range body.Groups {
		if group.Level != "Primary" {
			continue
		}
		for _, school := range group.Schools {
			if strings.Contains(strings.ToLower(school.Name), lowerKeyword) {
				ids = append(ids, strconv.Itoa(school.ID))
			}
		}
	}

	return ids, nil
}

func searchProjectsInMap(mapRequest MapRequest) (MapResponse, error) {
	baseUrl := os.Getenv("BASE_URL")

	reqBody := map[string]any{
		"selectedLocation": map[string]any{
			"locationType": "anywhere",
		},
		"rentalTransactionFilter": map[string]any{
			"contractDateRangeType": defaultDateRange,
		},
		"propertyFilter": map[string]any{
			"propertyType":       "Condo",
			"unitBedroomTypes":   mapRequest.UnitBedroomTypes,
			"isNewLaunchProject": mapRequest.IsNewLaunch,
		},
		"saleTransactionFilter": map[string]any{
			"contractDateRangeType": defaultDateRange,
		},
		"expectedLocationType": "project",
		"pageSize":             20,
		"page":                 1,
		"sortOrder":            "desc",
	}

	if len(mapRequest.LocationIDs) > 0 && mapRequest.LocationType != "" {
		reqBody["selectedLocation"] = map[string]any{
			"locationIDs":  mapRequest.LocationIDs,
			"locationType": mapRequest.LocationType,
			"radius":       mapRequest.Radius,
		}
	}

	if mapRequest.TransactionDateRange != "" {
		reqBody["rentalTransactionFilter"] = map[string]any{
			"contractDateRangeType": mapRequest.TransactionDateRange,
		}

		reqBody["saleTransactionFilter"] = map[string]any{
			"contractDateRangeType": mapRequest.TransactionDateRange,
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return MapResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		baseUrl+"/api/property-analysis/search", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return MapResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+os.Getenv("API_TOKEN"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return MapResponse{}, fmt.Errorf("failed to call property-analysis API: %w", err)
	}
	defer resp.Body.Close()

	var rawResp struct {
		Data []struct {
			Location struct {
				Location struct {
					ID           int    `json:"id"`
					Name         string `json:"name"`
					PropertyType string `json:"propertyType"`
					District     string `json:"district"`
				} `json:"location"`
			} `json:"location"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawResp); err != nil {
		return MapResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}

	var result MapResponse
	for _, item := range rawResp.Data {
		result.Data = append(result.Data, MapItem{
			Location: struct {
				ID           int    `json:"id"`
				Name         string `json:"name"`
				PropertyType string `json:"propertyType"`
				District     string `json:"district"`
			}{
				ID:           item.Location.Location.ID,
				Name:         item.Location.Location.Name,
				PropertyType: item.Location.Location.PropertyType,
				District:     item.Location.Location.District,
			},
		})
	}

	return result, nil
}
