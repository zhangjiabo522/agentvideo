package app

type SceneNode struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Name       string    `json:"name"`
	X          float64   `json:"x"`
	Y          float64   `json:"y"`
	Width      float64   `json:"width"`
	Height     float64   `json:"height"`
	Rotation   float64   `json:"rotation"`
	Opacity    float64   `json:"opacity"`
	Color      string    `json:"color"`
	Text       string    `json:"text,omitempty"`
	FontSize   float64   `json:"fontSize,omitempty"`
	FontWeight int       `json:"fontWeight,omitempty"`
	Src        string    `json:"src,omitempty"`
	ObjectFit  string    `json:"objectFit,omitempty"`
	Protected  []string  `json:"protected,omitempty"`
	Data       []float64 `json:"data,omitempty"`
	Labels     []string  `json:"labels,omitempty"`
	Start      int       `json:"start"`
	End        int       `json:"end"`
	Animation  string    `json:"animation"`
	Locked     bool      `json:"locked"`
	Hidden     bool      `json:"hidden"`
}

type Scene struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Duration   int         `json:"duration"`
	Background string      `json:"background"`
	Notes      string      `json:"notes"`
	Nodes      []SceneNode `json:"nodes"`
}

type AudioTrack struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Src            string  `json:"src"`
	Start          int     `json:"start"`
	TrimStart      float64 `json:"trimStart"`
	Duration       int     `json:"duration"`
	SourceDuration int     `json:"sourceDuration,omitempty"`
	Volume         float64 `json:"volume"`
	FadeIn         int     `json:"fadeIn,omitempty"`
	FadeOut        int     `json:"fadeOut,omitempty"`
}

type Project struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Width     int          `json:"width"`
	Height    int          `json:"height"`
	FPS       int          `json:"fps"`
	Revision  int          `json:"revision"`
	UpdatedAt string       `json:"updatedAt"`
	Scenes    []Scene      `json:"scenes"`
	Audio     []AudioTrack `json:"audio"`
}

type Settings struct {
	BaseURL        string  `json:"baseUrl"`
	Model          string  `json:"model"`
	APIKey         string  `json:"apiKey,omitempty"`
	HasAPIKey      bool    `json:"hasApiKey"`
	ImageBaseURL   string  `json:"imageBaseUrl"`
	ImageModel     string  `json:"imageModel"`
	ImageAPIKey    string  `json:"imageApiKey,omitempty"`
	HasImageAPIKey bool    `json:"hasImageApiKey"`
	TTSProvider    string  `json:"ttsProvider"`
	TTSBaseURL     string  `json:"ttsBaseUrl"`
	TTSModel       string  `json:"ttsModel"`
	TTSAPIKey      string  `json:"ttsApiKey,omitempty"`
	HasTTSAPIKey   bool    `json:"hasTtsApiKey"`
	TTSVoice       string  `json:"ttsVoice"`
	TTSSpeed       float64 `json:"ttsSpeed"`
}
