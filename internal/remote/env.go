package remote

import (
	"fmt"
	"net/url"
	"strings"
)

// DefaultHubBase is the clai cloud origin cchub: targets resolve against
// when CLAI_CLOUD_URL is unset.
const DefaultHubBase = "https://www.clai.chat"

// Env is the part of the environment remote targets read.
type Env struct {
	// CloudURL is CLAI_CLOUD_URL: the clai cloud origin, such as
	// https://dev.clai.chat. Empty means DefaultHubBase.
	CloudURL string
	// Cloud is CLAI_CLOUD; "off" makes every target cache-only.
	Cloud string
}

// EnvFrom reads Env through getenv, os.Getenv in production.
func EnvFrom(getenv func(string) string) Env {
	return Env{CloudURL: getenv("CLAI_CLOUD_URL"), Cloud: getenv("CLAI_CLOUD")}
}

// HubBase validates CloudURL and returns the origin to use.
func (e Env) HubBase() (string, error) {
	if strings.TrimSpace(e.CloudURL) == "" {
		return DefaultHubBase, nil
	}
	u, err := url.Parse(strings.TrimSpace(e.CloudURL))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("CLAI_CLOUD_URL must be an https origin, got %q", e.CloudURL)
	}
	return "https://" + u.Host, nil
}

// Offline reports CLAI_CLOUD=off.
func (e Env) Offline() bool {
	return strings.EqualFold(strings.TrimSpace(e.Cloud), "off")
}

// NewResolver builds a resolver over the config and cache directories.
func NewResolver(env Env, configDir, cacheDir string, client Doer, prompt Prompter, note func(string)) (Resolver, error) {
	base, err := env.HubBase()
	if err != nil {
		return Resolver{}, err
	}
	return Resolver{
		Client:  client,
		HubBase: base,
		Offline: env.Offline(),
		Trust:   NewTrustStore(configDir),
		Cache:   NewCache(cacheDir),
		Prompt:  prompt,
		Note:    note,
	}, nil
}
