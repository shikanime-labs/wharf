// CLI to build and push OCI images from Nix flakes
package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/spf13/viper"
)

func init() {
	viper.AutomaticEnv()
	if err := viper.BindEnv("build_context", "BUILD_CONTEXT"); err != nil {
		slog.Error("bind env failed", "env", "BUILD_CONTEXT", "key", "build_context", "err", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("image", "IMAGE"); err != nil {
		slog.Error("bind env failed", "env", "IMAGE", "key", "image", "err", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("platforms", "PLATFORMS"); err != nil {
		slog.Error("bind env failed", "env", "PLATFORMS", "key", "platforms", "err", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("push_image", "PUSH_IMAGE"); err != nil {
		slog.Error("bind env failed", "env", "PUSH_IMAGE", "key", "push_image", "err", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("log_level", "LOG_LEVEL"); err != nil {
		slog.Error("bind env failed", "env", "LOG_LEVEL", "key", "log_level", "err", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("debug", "DEBUG"); err != nil {
		slog.Error("bind env failed", "env", "DEBUG", "key", "debug", "err", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("flake", "FLAKE"); err != nil {
		slog.Error("bind env failed", "env", "FLAKE", "key", "flake", "err", err)
		os.Exit(1)
	}
}

func getHostPlatform() *v1.Platform {
	return &v1.Platform{OS: "linux", Architecture: runtime.GOARCH}
}

func parsePlatform(s string) *v1.Platform {
	seg := strings.SplitN(s, "/", 2)
	operatingSystem := ""
	arch := ""
	if len(seg) > 0 {
		operatingSystem = seg[0]
	}
	if len(seg) > 1 {
		arch = seg[1]
	}
	return &v1.Platform{OS: operatingSystem, Architecture: arch}
}

func getPlatforms() []*v1.Platform {
	v := viper.GetString("platforms")
	if v == "" {
		hp := getHostPlatform()
		slog.Info("no platforms specified", "detected_os", hp.OS, "detected_arch", hp.Architecture)
		return []*v1.Platform{hp}
	}
	ps := strings.Split(v, ",")
	plats := make([]*v1.Platform, 0, len(ps))
	for _, s := range ps {
		p := parsePlatform(s)
		if slices.ContainsFunc(plats, func(existing *v1.Platform) bool {
			return existing.OS == p.OS && existing.Architecture == p.Architecture
		}) {
			slog.Warn("duplicate platform skipped", "platform", p.OS+"/"+p.Architecture)
			continue
		}
		plats = append(plats, p)
	}
	return plats
}

func getPushImage() bool {
	switch strings.ToLower(viper.GetString("push_image")) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func getBuildContext() string {
	return viper.GetString("build_context")
}

func getImageTag() (name.Tag, error) {
	s := viper.GetString("image")
	ref, err := name.NewTag(s)
	if err != nil {
		return name.Tag{}, fmt.Errorf("invalid image reference: %w", err)
	}
	return ref, nil
}

func getLogLevel() (slog.Level, error) {
	v := strings.ToLower(viper.GetString("log_level"))
	switch v {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error", "err":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level: %s", v)
	}
}

func getDebug() bool {
	return viper.GetBool("debug") || viper.GetBool("actions_step_debug")
}

func getFlakeURL() string {
	return viper.GetString("flake")
}

// getExtraOptions parses repeated `--option key=value` flags into
// key/value pairs forwarded to the underlying nix command.
func getExtraOptions() ([][2]string, error) {
	raw := viper.GetStringSlice("options")
	pairs := make([][2]string, 0, len(raw))
	for _, item := range raw {
		key, value, found := strings.Cut(item, "=")
		if !found || key == "" || value == "" {
			return nil, fmt.Errorf("invalid --option %q: expected key=value", item)
		}
		pairs = append(pairs, [2]string{key, value})
	}
	return pairs, nil
}
