package tools

// The reference offers a marketplace of ready-made toolkits, grouped by what
// they are for. This is the same idea at the size Cosmo can honestly support:
// public APIs that need no key, each with its actions already described, so
// installing one produces a tool that works on the first call rather than a
// shell to fill in.
//
// Everything here is public and unauthenticated on purpose. An entry that
// needed a key would install something unusable until someone found the key,
// which is the opposite of what a catalogue is for.
//
// What is deliberately absent: install counts and popularity. The reference
// shows them because it has many deployments to count across; inventing them
// here would be fabricated social proof.
type CatalogEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Category    string `json:"category"`
	// "http" for an API, "builtin" for one that reaches nothing and does the
	// work in this process.
	Kind    string   `json:"kind"`
	BaseURL string   `json:"base_url"`
	Actions []Action `json:"actions"`
}

// Categories in the order the marketplace shows them: what runs without a
// network first, then the groups by subject.
const (
	CategoryBuiltin   = "Built-in"
	CategoryDeveloper = "Developer tools"
	CategoryReference = "Reference"
	CategoryPlaces    = "Weather and places"
	CategoryFinance   = "Finance"
	CategorySchedule  = "Dates and places"
	CategoryScience   = "Science and data"
)

// CatalogCategories is the order the marketplace lists them in. Kept beside
// the entries so a new category cannot be added without deciding where it goes.
func CatalogCategories() []string {
	return []string{
		CategoryBuiltin,
		CategoryDeveloper,
		CategoryReference,
		CategoryPlaces,
		CategoryFinance,
		CategorySchedule,
		CategoryScience,
	}
}

func text(name, description, in string, required bool) Parameter {
	return Parameter{Name: name, Description: description, Type: "string", In: in, IsRequired: required}
}

func number(name, description, in string, required bool) Parameter {
	return Parameter{Name: name, Description: description, Type: "number", In: in, IsRequired: required}
}

// fixed is a query parameter the tool always sends and the model never sees:
// a units flag, a result count, a response format. Written here rather than
// into the path, so the path stays a path and the settings stay readable.
func fixed(name, description, value string) Parameter {
	return Parameter{
		Name:        name,
		Description: description,
		Type:        "string",
		In:          "query",
		Source:      SourceFixed,
		Value:       value,
	}
}

// Catalog returns the entries on offer. A function rather than a variable so a
// caller cannot mutate the shared list by accident.
func Catalog() []CatalogEntry {
	return []CatalogEntry{
		// ---------------------------------------------------------------
		// Built-in: no endpoint, no credential, no network.
		// ---------------------------------------------------------------
		{
			ID:          "calculator",
			Name:        "Calculator",
			Description: "Arithmetic that is right every time, which a model on its own is not",
			Icon:        "🧮",
			Category:    CategoryBuiltin,
			Kind:        KindBuiltin,
			Actions: []Action{{
				Name:              "calculate",
				Description:       "Evaluate an arithmetic expression: + - * / %, brackets, decimals",
				ResultType:        "number",
				ResultDescription: "The value of the expression.",
				Method:            "POST",
				Path:              "/",
				Parameters:        []Parameter{text("expression", "For example (120 + 80) * 3", "body", true)},
			}},
		},
		{
			ID:          "clock",
			Name:        "Clock",
			Description: "The current date and time in any timezone",
			Icon:        "🕐",
			Category:    CategoryBuiltin,
			Kind:        KindBuiltin,
			Actions: []Action{{
				Name:              "current_time",
				Description:       "The time now in a named timezone",
				ResultType:        "string",
				ResultDescription: "The current time in the timezone asked for, as an ISO 8601 timestamp.",
				Method:            "POST",
				Path:              "/",
				Parameters:        []Parameter{text("timezone", "An IANA name such as Asia/Ho_Chi_Minh; UTC if omitted", "body", false)},
			}},
		},
		{
			ID:          "data",
			Name:        "Data",
			Description: "Exact arithmetic over a column of numbers: total, mean, median, spread",
			Icon:        "📊",
			Category:    CategoryBuiltin,
			Kind:        KindBuiltin,
			Actions: []Action{{
				Name:              "describe_numbers",
				Description:       "Summarise a list of numbers: count, sum, mean, median, min, max, range, standard deviation, and which label holds the highest and lowest value",
				ResultType:        "object",
				ResultDescription: "Returns object: count, sum, mean, median, min, max, range, stddev, and highest_label/lowest_label when labels were given.",
				Method:            "POST",
				Path:              "/",
				Parameters: []Parameter{
					text("values", "The numbers, as a JSON array or separated by commas", "body", true),
					text("labels", "What each number is, in the same order - optional, and only used to name the highest and lowest", "body", false),
				},
			}},
		},
		{
			ID:          "chart",
			Name:        "Chart",
			Description: "Draw the numbers: bar, line, area, pie and their variants",
			Icon:        "📈",
			Category:    CategoryBuiltin,
			Kind:        KindBuiltin,
			Actions: []Action{{
				Name: "draw_chart",
				Description: "Draw a chart from labels and values. Use it when a comparison, a trend or a share is easier seen than read. " +
					"Types: bar, hbar (horizontal, for long labels), line, area, pie, donut. " +
					"One series goes in values; several go in series as [{\"name\":\"…\",\"values\":[…]}].",
				ResultType:        "object",
				ResultDescription: "Returns object: the chart specification that was drawn - type, title, labels, series, is_stacked.",
				Method:            "POST",
				Path:              "/",
				Parameters: []Parameter{
					text("type", "bar, hbar, line, area, pie or donut - bar if omitted", "body", false),
					text("labels", "The axis: what each value is, as a JSON array or separated by commas", "body", true),
					text("values", "One series of numbers. Use series instead to draw several", "body", false),
					text("series", "Several series, as JSON: [{\"name\":\"2025\",\"values\":[1,2,3]}]", "body", false),
					text("series_name", "What the single series is called, shown in the legend", "body", false),
					text("title", "A short title above the chart", "body", false),
					text("stacked", "true to pile several series rather than set them side by side - bar, hbar and area only", "body", false),
				},
			}},
		},
		{
			ID:          "profile",
			Name:        "Profile",
			Description: "Who is asking: their name, address and role, and the workspace they are in",
			Icon:        "🪪",
			Category:    CategoryBuiltin,
			Kind:        KindBuiltin,
			Actions: []Action{{
				Name:              "current_user",
				Description:       "The person asking this question: name, email, role, and the workspace they are working in",
				ResultType:        "object",
				ResultDescription: "Returns object: name, email, role, workspace, workspace_role.",
				Method:            "POST",
				Path:              "/",
				// No parameters on purpose. Whose profile this is comes from
				// the session, not from the model.
				Parameters: []Parameter{},
			}},
		},

		// ---------------------------------------------------------------
		// Developer tools
		// ---------------------------------------------------------------
		{
			ID:          "github",
			Name:        "GitHub",
			Description: "Read public GitHub users, repositories and activity",
			Icon:        "🐙",
			Category:    CategoryDeveloper,
			Kind:        KindHTTP,
			BaseURL:     "https://api.github.com",
			Actions: []Action{
				{
					Name:              "get_user",
					Description:       "Read a GitHub user's public profile",
					ResultType:        "object",
					ResultDescription: "The profile: login, name, bio, public_repos, followers, created_at.",
					Method:            "GET",
					Path:              "/users/{username}",
					Parameters:        []Parameter{text("username", "The GitHub login, for example octocat", "path", true)},
				},
				{
					Name:              "list_user_repos",
					Description:       "List a GitHub user's public repositories, newest first",
					ResultType:        "array",
					ResultDescription: "One entry per repository with name, description, language, stargazers_count and updated_at.",
					Method:            "GET",
					Path:              "/users/{username}/repos",
					Parameters: []Parameter{text("username", "The GitHub login", "path", true),
						fixed("sort", "Newest first", "updated"),
						fixed("per_page", "How many repositories", "20")},
				},
				{
					Name:              "search_repositories",
					Description:       "Search public repositories by keyword",
					ResultType:        "object",
					ResultDescription: "total_count, and items[] each with full_name, description, language and stargazers_count.",
					Method:            "GET",
					Path:              "/search/repositories",
					Parameters:        []Parameter{text("q", "Search terms, for example: language:go http client", "query", true)},
				},
			},
		},
		{
			ID:          "hacker_news",
			Name:        "Hacker News",
			Description: "Front page stories and the discussion under them",
			Icon:        "📰",
			Category:    CategoryDeveloper,
			Kind:        KindHTTP,
			BaseURL:     "https://hacker-news.firebaseio.com",
			Actions: []Action{
				{
					Name:              "top_story_ids",
					Description:       "Ids of the current front-page stories, best first",
					ResultType:        "array",
					ResultDescription: "Story ids, most points first. Pass one to get_item to read it.",
					Method:            "GET",
					Path:              "/v0/topstories.json",
				},
				{
					Name:              "get_item",
					Description:       "One story or comment by id, including its title, score and text",
					ResultType:        "object",
					ResultDescription: "One story or comment: title, by, score, time, url, and kids[] of reply ids.",
					Method:            "GET",
					Path:              "/v0/item/{id}.json",
					Parameters:        []Parameter{text("id", "The item id, from top_story_ids", "path", true)},
				},
			},
		},
		{
			ID:          "npm",
			Name:        "npm registry",
			Description: "Versions, dependencies and metadata for a published package",
			Icon:        "📦",
			Category:    CategoryDeveloper,
			Kind:        KindHTTP,
			BaseURL:     "https://registry.npmjs.org",
			Actions: []Action{{
				Name:              "get_package",
				Description:       "Everything the registry knows about one package",
				ResultType:        "object",
				ResultDescription: "The package: name, description, dist-tags.latest, and versions keyed by number.",
				Method:            "GET",
				Path:              "/{package}",
				Parameters:        []Parameter{text("package", "The package name, for example react", "path", true)},
			}},
		},
		{
			ID:          "httpbin",
			Name:        "HTTP inspector",
			Description: "Echoes back what was sent - useful for proving a tool works at all",
			Icon:        "🔍",
			Category:    CategoryDeveloper,
			Kind:        KindHTTP,
			BaseURL:     "https://httpbin.org",
			Actions: []Action{
				{
					Name:              "echo_get",
					Description:       "Return the query parameters and headers that arrived",
					ResultType:        "object",
					ResultDescription: "What the server saw: args, headers, origin and url. For checking a request, not for facts.",
					Method:            "GET",
					Path:              "/get",
					Parameters:        []Parameter{text("message", "Anything; it comes back in the reply", "query", false)},
				},
				{
					Name:              "echo_post",
					Description:       "Return the JSON body that arrived",
					ResultType:        "object",
					ResultDescription: "What the server saw, including the body it was sent. For checking a request, not for facts.",
					Method:            "POST",
					Path:              "/post",
					Parameters:        []Parameter{text("message", "Anything; it comes back in the reply", "body", false)},
				},
			},
		},

		// ---------------------------------------------------------------
		// Reference
		// ---------------------------------------------------------------
		{
			ID:          "wikipedia",
			Name:        "Wikipedia",
			Description: "The opening summary of an English Wikipedia article",
			Icon:        "📖",
			Category:    CategoryReference,
			Kind:        KindHTTP,
			BaseURL:     "https://en.wikipedia.org",
			Actions: []Action{
				{
					Name:              "get_summary",
					Description:       "The opening summary of an article",
					ResultType:        "object",
					ResultDescription: "title, extract holding the opening paragraphs, description, and content_urls.",
					Method:            "GET",
					Path:              "/api/rest_v1/page/summary/{title}",
					Parameters:        []Parameter{text("title", "Article title, underscores for spaces", "path", true)},
				},
				{
					Name:              "search_articles",
					Description:       "Find articles matching a phrase",
					ResultType:        "object",
					ResultDescription: "query.search[] with title and a snippet of matching text per article.",
					Method:            "GET",
					Path:              "/w/api.php",
					Parameters: []Parameter{
						text("srsearch", "What to look for", "query", true),
						fixed("action", "MediaWiki action", "query"),
						fixed("list", "What to list", "search"),
						fixed("format", "Response format", "json"),
					},
				},
			},
		},
		{
			ID:          "dictionary",
			Name:        "Dictionary",
			Description: "Definitions, pronunciation and usage for an English word",
			Icon:        "🔤",
			Category:    CategoryReference,
			Kind:        KindHTTP,
			BaseURL:     "https://api.dictionaryapi.dev",
			Actions: []Action{{
				Name:              "define_word",
				Description:       "Definitions and examples for one word",
				ResultType:        "array",
				ResultDescription: "One entry per sense, each with meanings[] holding partOfSpeech and definitions[].",
				Method:            "GET",
				Path:              "/api/v2/entries/en/{word}",
				Parameters:        []Parameter{text("word", "A single English word", "path", true)},
			}},
		},
		{
			ID:          "open_library",
			Name:        "Open Library",
			Description: "Search books by title, author or subject",
			Icon:        "📚",
			Category:    CategoryReference,
			Kind:        KindHTTP,
			BaseURL:     "https://openlibrary.org",
			Actions: []Action{{
				Name:              "search_books",
				Description:       "Find books matching a query",
				ResultType:        "object",
				ResultDescription: "numFound, and docs[] each with title, author_name, first_publish_year.",
				Method:            "GET",
				Path:              "/search.json",
				Parameters: []Parameter{
					text("q", "Title, author or subject", "query", true),
					fixed("limit", "How many results", "10"),
				},
			}},
		},
		{
			ID:          "countries",
			Name:        "Countries",
			Description: "Facts about a country: capital, currency, languages, population",
			Icon:        "🌍",
			Category:    CategoryReference,
			Kind:        KindHTTP,
			BaseURL:     "https://restcountries.com",
			Actions: []Action{{
				Name:              "get_country",
				Description:       "Look up a country by name",
				ResultType:        "array",
				ResultDescription: "One entry per match: name, capital, region, population, currencies and languages.",
				Method:            "GET",
				Path:              "/v3.1/name/{name}",
				Parameters:        []Parameter{text("name", "Country name, for example Vietnam", "path", true)},
			}},
		},

		// ---------------------------------------------------------------
		// Weather and places
		// ---------------------------------------------------------------
		{
			ID:          "weather",
			Name:        "Weather",
			Description: "Current conditions and forecast for any coordinates",
			Icon:        "🌤",
			Category:    CategoryPlaces,
			Kind:        KindHTTP,
			BaseURL:     "https://api.open-meteo.com",
			Actions: []Action{{
				Name:              "get_forecast",
				Description:       "Weather forecast for a latitude and longitude",
				ResultType:        "object",
				ResultDescription: "current.temperature_2m in Celsius, current.relative_humidity_2m, current.weather_code, and the coordinates the reading is for.",
				Method:            "GET",
				Path:              "/v1/forecast",
				Parameters: []Parameter{
					number("latitude", "Latitude in decimal degrees", "query", true),
					number("longitude", "Longitude in decimal degrees", "query", true),
					fixed("current", "Readings to return", "temperature_2m,relative_humidity_2m,weather_code"),
					fixed("forecast_days", "How many days ahead", "3"),
				},
			}},
		},
		{
			ID:          "place_search",
			Name:        "Place search",
			Description: "Turn a place name into coordinates - pairs with Weather",
			Icon:        "📍",
			Category:    CategoryPlaces,
			Kind:        KindHTTP,
			BaseURL:     "https://geocoding-api.open-meteo.com",
			Actions: []Action{{
				Name:              "find_place",
				Description:       "Coordinates, country and population for a place name",
				ResultType:        "object",
				ResultDescription: "results[] each with name, latitude, longitude, country and population. Empty when nothing matches.",
				Method:            "GET",
				Path:              "/v1/search",
				Parameters: []Parameter{
					text("name", "Place name, for example Quang Ngai", "query", true),
					fixed("count", "How many matches", "5"),
					fixed("format", "Response format", "json"),
				},
			}},
		},
		{
			ID:          "sun_times",
			Name:        "Sunrise and sunset",
			Description: "Daylight times for a set of coordinates",
			Icon:        "🌅",
			Category:    CategoryPlaces,
			Kind:        KindHTTP,
			BaseURL:     "https://api.sunrise-sunset.org",
			Actions: []Action{{
				Name:              "get_sun_times",
				Description:       "Sunrise, sunset and day length for one day",
				ResultType:        "object",
				ResultDescription: "results with sunrise, sunset, solar_noon and day_length. Times are UTC unless a timezone was asked for.",
				Method:            "GET",
				Path:              "/json",
				Parameters: []Parameter{
					number("lat", "Latitude in decimal degrees", "query", true),
					number("lng", "Longitude in decimal degrees", "query", true),
					text("date", "A date as YYYY-MM-DD, or today if omitted", "query", false),
					fixed("formatted", "Return raw timestamps rather than prose", "0"),
				},
			}},
		},

		// ---------------------------------------------------------------
		// Finance
		// ---------------------------------------------------------------
		{
			ID:          "exchange_rates",
			Name:        "Exchange rates",
			Description: "Reference exchange rates published by the European Central Bank",
			Icon:        "💱",
			Category:    CategoryFinance,
			Kind:        KindHTTP,
			BaseURL:     "https://api.frankfurter.app",
			Actions: []Action{
				{
					Name:              "get_latest_rates",
					Description:       "Latest rates for one base currency",
					ResultType:        "object",
					ResultDescription: "base, date, and rates keyed by currency code.",
					Method:            "GET",
					Path:              "/latest",
					Parameters: []Parameter{
						text("base", "Three-letter base currency, for example USD", "query", false),
						text("symbols", "Comma-separated currencies to return, for example VND,EUR", "query", false),
					},
				},
				{
					Name:              "get_rates_on",
					Description:       "Rates as they stood on a given date",
					ResultType:        "object",
					ResultDescription: "base, the date asked for, and rates keyed by currency code.",
					Method:            "GET",
					Path:              "/{date}",
					Parameters: []Parameter{
						text("date", "A date as YYYY-MM-DD", "path", true),
						text("base", "Three-letter base currency", "query", false),
					},
				},
			},
		},

		// ---------------------------------------------------------------
		// Dates and places
		// ---------------------------------------------------------------
		{
			ID:          "public_holidays",
			Name:        "Public holidays",
			Description: "Official public holidays for a country and year",
			Icon:        "📅",
			Category:    CategorySchedule,
			Kind:        KindHTTP,
			BaseURL:     "https://date.nager.at",
			Actions: []Action{{
				Name:              "list_holidays",
				Description:       "Public holidays for one country in one year",
				ResultType:        "array",
				ResultDescription: "One entry per holiday: date, localName, name, and whether it is nationwide.",
				Method:            "GET",
				Path:              "/api/v3/PublicHolidays/{year}/{country}",
				Parameters: []Parameter{
					text("year", "A four-digit year", "path", true),
					text("country", "Two-letter country code, for example VN", "path", true),
				},
			}},
		},
		{
			ID:          "postal_codes",
			Name:        "Postal codes",
			Description: "The place a postal code refers to",
			Icon:        "✉️",
			Category:    CategorySchedule,
			Kind:        KindHTTP,
			BaseURL:     "https://api.zippopotam.us",
			Actions: []Action{{
				Name:              "lookup_postal_code",
				Description:       "Places covered by one postal code",
				ResultType:        "object",
				ResultDescription: "places[] each with 'place name', state and coordinates. 404 when the code does not exist.",
				Method:            "GET",
				Path:              "/{country}/{code}",
				Parameters: []Parameter{
					text("country", "Two-letter country code, for example us", "path", true),
					text("code", "The postal code", "path", true),
				},
			}},
		},

		// ---------------------------------------------------------------
		// Science and data
		// ---------------------------------------------------------------
		{
			ID:          "earthquakes",
			Name:        "Earthquakes",
			Description: "Recent earthquakes recorded by the US Geological Survey",
			Icon:        "🌋",
			Category:    CategoryScience,
			Kind:        KindHTTP,
			BaseURL:     "https://earthquake.usgs.gov",
			Actions: []Action{{
				Name:              "recent_earthquakes",
				Description:       "Earthquakes in a period, strongest first",
				ResultType:        "object",
				ResultDescription: "features[] each with properties.mag, properties.place, properties.time and coordinates.",
				Method:            "GET",
				Path:              "/fdsnws/event/1/query",
				Parameters: []Parameter{
					text("starttime", "Start date as YYYY-MM-DD", "query", false),
					number("minmagnitude", "Smallest magnitude to include", "query", false),
					fixed("format", "Response format", "geojson"),
					fixed("orderby", "Strongest first", "magnitude"),
					fixed("limit", "How many events", "20"),
				},
			}},
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
