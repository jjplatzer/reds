package net

import (
	"os"
	"strings"
)

const (
	PublicSmesWebSocketURL  = "wss://reds-stdds-live.jjplatzer.com/ws"
	PublicTaisWebSocketURL  = "wss://reds-stdds-live.jjplatzer.com/tais/ws"
	PublicAPTCWebSocketURL  = "wss://reds-stdds-live.jjplatzer.com/aptc/ws"
	PublicITWSWebSocketURL  = "wss://reds-stdds-live.jjplatzer.com/itws/{tracon}/ws"
	PublicSfdpsWebSocketURL = "wss://reds-stdds-live.jjplatzer.com/sfdps/ws"
	PublicPlaybackBaseURL   = "https://reds-stdds-live.jjplatzer.com/playback"
)

func UsePublicServer() bool {
	return boolEnv("USE_PUBLIC_SERVER", true)
}

func SmesWebSocketURL() string {
	if UsePublicServer() {
		return PublicSmesWebSocketURL
	}

	port := strings.TrimSpace(os.Getenv("WS_PORT"))
	if port == "" {
		port = "8080"
	}
	return "ws://localhost:" + port + "/ws"
}

func TaisWebSocketURL() string {
	if override := strings.TrimSpace(os.Getenv("TAIS_WS_URL")); override != "" {
		return override
	}

	if UsePublicServer() {
		return PublicTaisWebSocketURL
	}

	port := strings.TrimSpace(os.Getenv("TAIS_WS_PORT"))
	if port == "" {
		port = "8081"
	}
	return "ws://localhost:" + port + "/tais/ws"
}

func SfdpsWebSocketURL() string {
	if override := strings.TrimSpace(os.Getenv("SFDPS_WS_URL")); override != "" {
		return override
	}

	if UsePublicServer() {
		return PublicSfdpsWebSocketURL
	}

	port := strings.TrimSpace(os.Getenv("SFDPS_WS_PORT"))
	if port == "" {
		port = "8082"
	}
	return "ws://localhost:" + port + "/sfdps/ws"
}

func APTCWebSocketURL() string {
	if override := strings.TrimSpace(os.Getenv("APTC_WS_URL")); override != "" {
		return override
	}
	if UsePublicServer() {
		return PublicAPTCWebSocketURL
	}
	port := strings.TrimSpace(os.Getenv("APTC_WS_PORT"))
	if port == "" {
		port = "8083"
	}
	return "ws://localhost:" + port + "/aptc/ws"
}

func PlaybackBaseURL() string {
	if override := strings.TrimSpace(os.Getenv("PLAYBACK_BASE_URL")); override != "" {
		return strings.TrimRight(override, "/")
	}

	if UsePublicServer() {
		return PublicPlaybackBaseURL
	}

	port := strings.TrimSpace(os.Getenv("WS_PORT"))
	if port == "" {
		port = "8080"
	}
	return "http://localhost:" + port + "/playback"
}

// ITWSWebSocketURL selects the TRACON's precipitation feed. Overrides may
// contain {tracon}; a shared local server also accepts a site query parameter.
func ITWSWebSocketURL(tracon string) string {
	tracon = strings.ToLower(strings.TrimSpace(tracon))
	if override := strings.TrimSpace(os.Getenv("ITWS_WS_URL")); override != "" {
		return strings.ReplaceAll(override, "{tracon}", tracon)
	}
	if UsePublicServer() {
		return strings.ReplaceAll(PublicITWSWebSocketURL, "{tracon}", tracon)
	}
	port := strings.TrimSpace(os.Getenv("ITWS_WS_PORT"))
	if port == "" {
		port = "8084"
	}
	return "ws://localhost:" + port + "/itws/ws?site=" + strings.ToUpper(tracon)
}

func boolEnv(key string, def bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return def
	}

	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
