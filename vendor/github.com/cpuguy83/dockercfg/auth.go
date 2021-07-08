package dockercfg

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// This is used by the docker CLI in casses where an oauth identity token is used.
// In that case the username is stored litterally as `<token>`
// When fetching the credentials we check for this value to determine if
const tokenUsername = "<token>"

// GetRegistryCredentials gets registry credentials for the passed in registry host.
//
// This will use `LoadDefaultConfig` to read registry auth details from the config.
// If the config doesn't exist, it will attempt to load registry credentials using the default credential helper for the platform.
func GetRegistryCredentials(hostname string) (string, string, error) {
	cfg, err := LoadDefaultConfig()
	if err != nil {
		if !os.IsNotExist(err) {
			return "", "", err
		}
		return GetCredentialsFromHelper("", hostname)
	}
	return cfg.GetRegistryCredentials(hostname)
}

// ResolveRegistryHost can be used to transform a docker registry host name into what is used for the docker config/cred helpers
//
// This is useful for using with containerd authorizers.
// Natrually this only transforms docker hub URLs.
func ResolveRegistryHost(host string) string {
	switch host {
	case "index.docker.io", "docker.io", "https://index.docker.io/v1/", "registry-1.docker.io":
		return "https://index.docker.io/v1/"
	}
	return host
}

// GetRegistryCredentials gets credentials, if any, for the provided hostname
//
// Hostnames should already be resolved using `ResolveRegistryAuth`
//
// If the returned username string is empty, the password is an identity token.
func (c *Config) GetRegistryCredentials(hostname string) (string, string, error) {
	h, ok := c.CredentialHelpers[hostname]
	if ok {
		return GetCredentialsFromHelper(h, hostname)
	}

	if c.CredentialsStore != "" {
		return GetCredentialsFromHelper(c.CredentialsStore, hostname)
	}

	auth, ok := c.AuthConfigs[hostname]
	if !ok {
		return GetCredentialsFromHelper("", hostname)
	}

	if auth.IdentityToken != "" {
		return "", auth.IdentityToken, nil
	}

	if auth.Username != "" && auth.Password != "" {
		return auth.Username, auth.Password, nil
	}

	return DecodeBase64Auth(auth)
}

// DecodeBase64Auth decodes the legacy file-based auth storage from the docker CLI.
// It takes the "Auth" filed from AuthConfig and decodes that into a username and password.
//
// If "Auth" is empty, an empty user/pass will be returned, but not an error.
func DecodeBase64Auth(auth AuthConfig) (string, string, error) {
	if auth.Auth == "" {
		return "", "", nil
	}

	decLen := base64.StdEncoding.DecodedLen(len(auth.Auth))
	decoded := make([]byte, decLen)
	n, err := base64.StdEncoding.Decode(decoded, []byte(auth.Auth))
	if err != nil {
		return "", "", fmt.Errorf("error decoding auth from file: %w", err)
	}

	if n != decLen {
		return "", "", fmt.Errorf("decoded value does not match expected length, expected: %d, actual: %d", decLen, n)
	}

	split := strings.SplitN(string(decoded), ":", 2)
	if len(split) != 2 {
		return "", "", errors.New("invalid auth string")
	}

	return split[0], strings.Trim(split[1], "\x00"), nil
}

// Errors from credential helpers
var (
	ErrCredentialsNotFound         = errors.New("credentials not found in native keychain")
	ErrCredentialsMissingServerURL = errors.New("no credentials server URL")
)

// GetCredentialsFromHelper attempts to lookup credentials from the passed in docker credential helper.
//
// The credential helpoer should just be the suffix name (no "docker-credential-").
// If the passed in helper program is empty this will look up the default helper for the platform.
//
// If the credentials are not found, no error is returned, only empty credentials.
//
// Hostnames should already be resolved using `ResolveRegistryAuth`
//
// If the username string is empty, the password string is an identity token.
func GetCredentialsFromHelper(helper, hostname string) (string, string, error) {
	if helper == "" {
		helper = getCredentialHelper()
	}
	if helper == "" {
		return "", "", nil
	}

	p, err := exec.LookPath("docker-credential-" + helper)
	if err != nil {
		return "", "", nil
	}

	cmd := exec.Command(p, "get")
	cmd.Stdin = strings.NewReader(hostname)

	b, err := cmd.Output()
	if err != nil {
		s := strings.TrimSpace(string(b))

		switch s {
		case ErrCredentialsNotFound.Error():
			return "", "", nil
		case ErrCredentialsMissingServerURL.Error():
			return "", "", errors.New(s)
		default:
		}

		return "", "", err
	}

	var creds struct {
		Username string
		Secret   string
	}

	if err := json.Unmarshal(b, &creds); err != nil {
		return "", "", err
	}

	// When tokenUsername is used, the output is an identity token and the username is garbage
	if creds.Username == tokenUsername {
		creds.Username = ""
	}

	return creds.Username, creds.Secret, nil
}

// getCredentialHelper gets the default credential helper name for the current platform.
func getCredentialHelper() string {
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("pass"); err == nil {
			return "pass"
		}
		return "secretservice"
	case "darwin":
		return "osxkeychain"
	case "windows":
		return "wincred"
	default:
		return ""
	}
}
