package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Open-Meteo: free, no key. Data is CC BY 4.0 and must be attributed.
const (
	openMeteoGeocodeURL  = "https://geocoding-api.open-meteo.com/v1/search"
	openMeteoForecastURL = "https://api.open-meteo.com/v1/forecast"
)

// fetchWeather finds the place named in the request and returns its current
// weather and a three-day forecast. A place it can't find is an error, never a guess.
func fetchWeather(ctx context.Context, in map[string]any) (map[string]any, error) {
	place := ""
	for _, k := range []string{"location", "city", "q", "place"} {
		if v, ok := in[k].(string); ok && strings.TrimSpace(v) != "" {
			place = strings.TrimSpace(v)
			break
		}
	}
	if place == "" {
		return nil, errors.New(`the request has no "location"`)
	}

	var geo struct {
		Results []struct {
			Name      string  `json:"name"`
			Admin1    string  `json:"admin1"`
			Country   string  `json:"country"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := getJSON(ctx, openMeteoGeocodeURL+"?count=1&language=en&format=json&name="+url.QueryEscape(place), &geo); err != nil {
		return nil, fmt.Errorf("open-meteo geocoding: %w", err)
	}
	if len(geo.Results) == 0 {
		return nil, fmt.Errorf("open-meteo doesn't know a place called %q", place)
	}
	g := geo.Results[0]

	q := url.Values{}
	q.Set("latitude", fmt.Sprint(g.Latitude))
	q.Set("longitude", fmt.Sprint(g.Longitude))
	q.Set("current", "temperature_2m,apparent_temperature,relative_humidity_2m,precipitation,weather_code,wind_speed_10m")
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max")
	q.Set("forecast_days", "3")
	q.Set("timezone", "auto")
	var fc struct {
		Timezone string `json:"timezone"`
		Current  struct {
			Time        string  `json:"time"`
			Temp        float64 `json:"temperature_2m"`
			FeelsLike   float64 `json:"apparent_temperature"`
			Humidity    float64 `json:"relative_humidity_2m"`
			Precip      float64 `json:"precipitation"`
			WeatherCode int     `json:"weather_code"`
			Wind        float64 `json:"wind_speed_10m"`
		} `json:"current"`
		Daily struct {
			Time        []string  `json:"time"`
			WeatherCode []int     `json:"weather_code"`
			Max         []float64 `json:"temperature_2m_max"`
			Min         []float64 `json:"temperature_2m_min"`
			RainChance  []float64 `json:"precipitation_probability_max"`
		} `json:"daily"`
	}
	if err := getJSON(ctx, openMeteoForecastURL+"?"+q.Encode(), &fc); err != nil {
		return nil, fmt.Errorf("open-meteo forecast: %w", err)
	}

	days := []map[string]any{}
	for i, d := range fc.Daily.Time {
		if i >= len(fc.Daily.WeatherCode) || i >= len(fc.Daily.Max) || i >= len(fc.Daily.Min) {
			break
		}
		day := map[string]any{"date": d, "conditions": wmoText(fc.Daily.WeatherCode[i]), "max_c": fc.Daily.Max[i], "min_c": fc.Daily.Min[i]}
		if i < len(fc.Daily.RainChance) {
			day["rain_chance_pct"] = fc.Daily.RainChance[i]
		}
		days = append(days, day)
	}
	name := g.Name
	if g.Admin1 != "" && g.Admin1 != g.Name {
		name += ", " + g.Admin1
	}
	if g.Country != "" {
		name += ", " + g.Country
	}
	return map[string]any{
		"location": map[string]any{"name": name, "latitude": g.Latitude, "longitude": g.Longitude, "timezone": fc.Timezone},
		"current": map[string]any{
			"time": fc.Current.Time, "conditions": wmoText(fc.Current.WeatherCode),
			"temperature_c": fc.Current.Temp, "feels_like_c": fc.Current.FeelsLike, "humidity_pct": fc.Current.Humidity,
			"precipitation_mm": fc.Current.Precip, "wind_kmh": fc.Current.Wind,
		},
		"forecast":    days,
		"source":      "Open-Meteo (live)",
		"attribution": "Weather data by Open-Meteo.com (CC BY 4.0)",
		"as_of":       time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	res, err := priceClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// wmoText is the WMO weather interpretation code in words.
func wmoText(code int) string {
	switch {
	case code == 0:
		return "clear sky"
	case code == 1:
		return "mainly clear"
	case code == 2:
		return "partly cloudy"
	case code == 3:
		return "overcast"
	case code == 45 || code == 48:
		return "fog"
	case code >= 51 && code <= 57:
		return "drizzle"
	case code >= 61 && code <= 67:
		return "rain"
	case code >= 71 && code <= 77:
		return "snow"
	case code >= 80 && code <= 82:
		return "rain showers"
	case code == 85 || code == 86:
		return "snow showers"
	case code == 95:
		return "thunderstorm"
	case code == 96 || code == 99:
		return "thunderstorm with hail"
	}
	return fmt.Sprintf("weather code %d", code)
}
