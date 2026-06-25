package net

import (
	"os"
	"strings"
)

const (
	PublicTargetWebSocketURL = "wss://reds-stdds-live.jjplatzer.com/ws"
	PublicPlaybackBaseURL    = "https://reds-stdds-live.jjplatzer.com/playback"
)

func UsePublicServer() bool {
	return boolEnv("USE_PUBLIC_SERVER", true)
}

func TargetWebSocketURL() string {
	if UsePublicServer() {
		return PublicTargetWebSocketURL
	}

	port := strings.TrimSpace(os.Getenv("WS_PORT"))
	if port == "" {
		port = "8080"
	}
	return "ws://localhost:" + port + "/ws"
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
