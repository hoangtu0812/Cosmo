package tools

// The reference offers a marketplace of ready-made tools. This is the same
// idea at the size Cosmo can honestly support: a short list of public APIs
// that need no key, each with its actions already described, so installing one
// produces a tool that works on the first call rather than a shell to fill in.
//
// Everything here is public and unauthenticated on purpose. A catalogue entry
// that needed a key would install something that cannot be used until someone
// finds the key, which is the opposite of what a catalogue is for.
type CatalogEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	BaseURL     string   `json:"base_url"`
	Actions     []Action `json:"actions"`
}

func text(name, description, in string, required bool) Parameter {
	return Parameter{Name: name, Description: description, Type: "string", In: in, IsRequired: required}
}

// Catalog returns the entries on offer. A function rather than a variable so a
// caller cannot mutate the shared list by accident.
func Catalog() []CatalogEntry {
	return []CatalogEntry{
		{
			ID:          "github",
			Name:        "GitHub",
			Description: "Read public GitHub users, repositories and activity",
			Icon:        "🐙",
			BaseURL:     "https://api.github.com",
			Actions: []Action{
				{
					Name:        "get_user",
					Description: "Read a GitHub user's public profile",
					Method:      "GET",
					Path:        "/users/{username}",
					Parameters:  []Parameter{text("username", "The GitHub login, for example octocat", "path", true)},
				},
				{
					Name:        "list_user_repos",
					Description: "List a GitHub user's public repositories, newest first",
					Method:      "GET",
					Path:        "/users/{username}/repos?sort=updated&per_page=20",
					Parameters:  []Parameter{text("username", "The GitHub login", "path", true)},
				},
				{
					Name:        "search_repositories",
					Description: "Search public repositories by keyword",
					Method:      "GET",
					Path:        "/search/repositories",
					Parameters: []Parameter{
						text("q", "Search terms, for example: language:go http client", "query", true),
					},
				},
			},
		},
		{
			ID:          "weather",
			Name:        "Weather",
			Description: "Current conditions and forecast for any coordinates",
			Icon:        "🌤",
			BaseURL:     "https://api.open-meteo.com",
			Actions: []Action{
				{
					Name:        "get_forecast",
					Description: "Weather forecast for a latitude and longitude",
					Method:      "GET",
					Path:        "/v1/forecast?current=temperature_2m,relative_humidity_2m,weather_code&forecast_days=3",
					Parameters: []Parameter{
						{Name: "latitude", Description: "Latitude in decimal degrees", Type: "number", In: "query", IsRequired: true},
						{Name: "longitude", Description: "Longitude in decimal degrees", Type: "number", In: "query", IsRequired: true},
					},
				},
			},
		},
		{
			ID:          "countries",
			Name:        "Countries",
			Description: "Facts about a country: capital, currency, languages, population",
			Icon:        "🌍",
			BaseURL:     "https://restcountries.com",
			Actions: []Action{
				{
					Name:        "get_country",
					Description: "Look up a country by name",
					Method:      "GET",
					Path:        "/v3.1/name/{name}",
					Parameters:  []Parameter{text("name", "Country name, for example Vietnam", "path", true)},
				},
			},
		},
		{
			ID:          "exchange_rates",
			Name:        "Exchange rates",
			Description: "Reference exchange rates published by the European Central Bank",
			Icon:        "💱",
			BaseURL:     "https://api.frankfurter.app",
			Actions: []Action{
				{
					Name:        "get_latest_rates",
					Description: "Latest rates for one base currency",
					Method:      "GET",
					Path:        "/latest",
					Parameters: []Parameter{
						text("base", "Three-letter base currency, for example USD", "query", false),
						text("symbols", "Comma-separated currencies to return, for example VND,EUR", "query", false),
					},
				},
			},
		},
		{
			ID:          "wikipedia",
			Name:        "Wikipedia",
			Description: "Summary of a Wikipedia article",
			Icon:        "📖",
			BaseURL:     "https://en.wikipedia.org",
			Actions: []Action{
				{
					Name:        "get_summary",
					Description: "The opening summary of an English Wikipedia article",
					Method:      "GET",
					Path:        "/api/rest_v1/page/summary/{title}",
					Parameters:  []Parameter{text("title", "Article title, underscores for spaces", "path", true)},
				},
			},
		},
	}
}

// CatalogEntryByID finds one entry, or reports that there is no such thing.
func CatalogEntryByID(id string) (CatalogEntry, bool) {
	for _, entry := range Catalog() {
		if entry.ID == id {
			return entry, true
		}
	}
	return CatalogEntry{}, false
}
