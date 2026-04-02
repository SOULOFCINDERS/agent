package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type WeatherTool struct {
	client *http.Client
}

func NewWeatherTool() *WeatherTool {
	return &WeatherTool{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (t *WeatherTool) Name() string { return "weather" }

func (t *WeatherTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	location := ""
	if v, ok := args["location"]; ok {
		location = fmt.Sprint(v)
	} else if v, ok := args["input"]; ok {
		location = fmt.Sprint(v)
	}

	if location == "" {
		return nil, fmt.Errorf("missing location")
	}

	// Clean up location (remove quotes etc)
	location = strings.Trim(location, "\"' ")

	// Use wttr.in for simple text weather
	// format=3 gives "Location: Condition Temp"
	url := fmt.Sprintf("https://wttr.in/%s?format=3", location)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	// wttr.in sometimes blocks default Go user agent
	req.Header.Set("User-Agent", "curl/7.64.1")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch weather: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather api returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return strings.TrimSpace(string(body)), nil
}
