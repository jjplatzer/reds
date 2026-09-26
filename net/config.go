package net

import (
	"os"
	"strings"
)

const (
	PublicSmesWebSocketURL = "wss://reds-stdds-live.jjplatzer.com/ws"
	PublicTaisWebSocketURL = "wss://reds-stdds-live.jjplatzer.com/tais/ws"
	PublicPlaybackBaseURL  = "https://reds-stdds-live.jjplatzer.com/playback"
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

func LiveServerToken() string {
	return strings.TrimSpace(os.Getenv("REDS_WS_TOKEN"))
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
