package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

type RegexRule struct {
	Pattern     *regexp.Regexp
	Replacement string
}

type Config struct {
	HomeserverURL string            `yaml:"homeserver_url"`
	Aliases       map[string]string `yaml:"aliases"`
	RawPatterns   yaml.MapSlice     `yaml:"patterns"`
	Patterns      []RegexRule       `yaml:"-"`
	CacheTTL      int64             `yaml:"cache_ttl"`

	Listen          string `yaml:"listen"`
	ServerWellKnown string `yaml:"server_well_known"`

	ServerKeys map[string]*ServerKey `yaml:"server_keys"`
}

type ServerKey struct {
	ServerName string `yaml:"server_name"`
	SigningKey string `yaml:"signing_key"`

	signingKeyID     string             `yaml:"-"`
	signingKeyPubB64 string             `yaml:"-"`
	signingKeyPriv   ed25519.PrivateKey `yaml:"-"`
}

type WellKnownResponse struct {
	ServerName string `json:"m.server"`
}

type ErrorResponse struct {
	Code    string `json:"errcode"`
	Message string `json:"error"`
}

const MNotFound = "M_NOT_FOUND"

type RoomDirectoryResponse struct {
	RoomID  string   `json:"room_id"`
	Servers []string `json:"servers"`

	FetchedAt int64 `json:"-"`
	Exists    bool  `json:"-"`
}

var cfg Config
var homeserverURL *url.URL
var cache = make(map[string]RoomDirectoryResponse)

func resolveAlias(alias string) (respData RoomDirectoryResponse) {
	existingData, ok := cache[alias]
	if ok && existingData.FetchedAt+cfg.CacheTTL > time.Now().Unix() {
		return existingData
	}
	resp, err := http.Get((&url.URL{
		Scheme:  homeserverURL.Scheme,
		User:    homeserverURL.User,
		Host:    homeserverURL.Host,
		Path:    fmt.Sprintf("/_matrix/client/v3/directory/room/%s", alias),
		RawPath: fmt.Sprintf("/_matrix/client/v3/directory/room/%s", url.PathEscape(alias)),
	}).String())
	if err != nil {
		fmt.Printf("Failed to resolve %s: %v\n", alias, err)
	} else if resp.StatusCode != http.StatusOK {
		fmt.Printf("Resolving %s responded with HTTP %d %s\n", alias, resp.StatusCode, resp.Status)
	} else if err = json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		fmt.Printf("Failed to parse response when resolving %s: %v\n", alias, err)
	} else {
		respData.Exists = true
		fmt.Printf("Successfully resolved %s -> %s with %d servers\n", alias, respData.RoomID, len(respData.Servers))
	}
	if existingData.Exists && !respData.Exists {
		fmt.Printf("Using expired cached result for %s as new resolving failed\n", alias)
		return existingData
	}
	respData.FetchedAt = time.Now().Unix()
	cache[alias] = respData
	return
}

func findAliasTarget(alias string) (string, bool) {
	target, ok := cfg.Aliases[alias]
	if ok {
		return target, true
	}
	for _, rule := range cfg.Patterns {
		if rule.Pattern.MatchString(alias) {
			return rule.Pattern.ReplaceAllString(alias, rule.Replacement), true
		}
	}
	return "", false
}

func queryDirectory(w http.ResponseWriter, req *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	alias := req.URL.Query().Get("room_alias")
	target, ok := findAliasTarget(alias)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Code:    MNotFound,
			Message: fmt.Sprintf("Room alias %s not found", alias),
		})
		return
	}
	resp := resolveAlias(target)
	if !resp.Exists {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Code:    MNotFound,
			Message: fmt.Sprintf("Failed to resolve %s", target),
		})
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func serverWellKnown(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(WellKnownResponse{ServerName: cfg.ServerWellKnown})
}

func serverVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"server": map[string]any{
			"name":    "mauliasproxy",
			"version": "0.2.0",
			"🐈‍⬛":     "https://github.com/tulir/mauliasproxy",
		},
	})
}

type ServerKeyResponse struct {
	OldVerifyKeys map[string]any               `json:"old_verify_keys"`
	ServerName    string                       `json:"server_name"`
	Signatures    map[string]map[string]string `json:"signatures,omitempty"`
	ValidUntilTS  int64                        `json:"valid_until_ts"`
	VerifyKeys    map[string]map[string]string `json:"verify_keys"`
}

func generateServerKey(domain string) *ServerKeyResponse {
	keys, ok := cfg.ServerKeys[domain]
	if !ok {
		keys, ok = cfg.ServerKeys["default"]
		if !ok {
			return nil
		}
	}
	var resp ServerKeyResponse
	if keys.ServerName == "" {
		resp.ServerName = domain
	} else {
		resp.ServerName = keys.ServerName
	}
	resp.OldVerifyKeys = map[string]any{}
	resp.ValidUntilTS = time.Now().Add(time.Hour * 24).UnixMilli()
	resp.VerifyKeys = map[string]map[string]string{
		keys.signingKeyID: {
			"key": keys.signingKeyPubB64,
		},
	}
	payload, _ := json.Marshal(&resp)
	resp.Signatures = map[string]map[string]string{
		resp.ServerName: {
			keys.signingKeyID: base64.RawURLEncoding.EncodeToString(ed25519.Sign(keys.signingKeyPriv, payload)),
		},
	}
	return &resp
}

func serverKey(w http.ResponseWriter, r *http.Request) {
	r.URL.Host = r.Host
	resp := generateServerKey(r.URL.Hostname())
	if resp == nil {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Code:    MNotFound,
			Message: "No server keys found",
		})
		return
	}
	w.Header().Add("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&resp)
}

func queryKey(w http.ResponseWriter, r *http.Request) {
	serverName := strings.TrimPrefix(r.URL.Path, "/_matrix/key/v2/query/")
	if strings.ContainsRune(serverName, '/') {
		notFound(w, r)
		return
	}
	resp := generateServerKey(serverName)
	if resp == nil {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Code:    MNotFound,
			Message: "No server keys found",
		})
		return
	}
	w.Header().Add("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&resp)
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Code:    MNotFound,
		Message: "This is a mauliasproxy instance that doesn't handle anything other than federation alias queries",
	})
}

func main() {
	if len(os.Args) > 0 && os.Args[len(os.Args)-1] == "genkey" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		fmt.Println("ed25519", base64.RawURLEncoding.EncodeToString(pub[:4]), base64.RawStdEncoding.EncodeToString(priv.Seed()))
		os.Exit(0)
	}
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to read config.yaml:", err)
		os.Exit(2)
	}
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to parse config.yaml:", err)
		os.Exit(3)
	}
	for host, key := range cfg.ServerKeys {
		if key.SigningKey == "ed25519 0 AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
			_, _ = fmt.Fprintln(os.Stderr, "Ignoring example server key")
			delete(cfg.ServerKeys, host)
			continue
		}
		parts := strings.Split(key.SigningKey, " ")
		if len(parts) != 3 || parts[0] != "ed25519" {
			_, _ = fmt.Fprintln(os.Stderr, "Invalid signing key for", key.ServerName)
			os.Exit(4)
		}
		decoded, err := base64.RawStdEncoding.DecodeString(parts[2])
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Invalid signing key for %s: %v\n", key.ServerName, err)
			os.Exit(4)
		}
		if key.ServerName == "" && host != "default" {
			key.ServerName = host
		}
		key.signingKeyID = "ed25519:" + parts[1]
		key.signingKeyPriv = ed25519.NewKeyFromSeed(decoded)
		key.signingKeyPubB64 = base64.RawStdEncoding.EncodeToString(key.signingKeyPriv.Public().(ed25519.PublicKey))
	}
	cfg.Patterns = make([]RegexRule, len(cfg.RawPatterns))
	for index, rawPattern := range cfg.RawPatterns {
		match := rawPattern.Key.(string)
		compiled, err := regexp.Compile(match)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Failed to compile pattern '%s': %v", match, err)
			os.Exit(4)
		}
		cfg.Patterns[index] = RegexRule{
			Pattern:     compiled,
			Replacement: rawPattern.Value.(string),
		}
	}
	homeserverURL, err = url.Parse(cfg.HomeserverURL)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to parse homeserver URL:", err)
		os.Exit(5)
	}

	http.HandleFunc("/_matrix/federation/v1/query/directory", queryDirectory)
	http.HandleFunc("/.well-known/matrix/server", serverWellKnown)
	http.HandleFunc("/_matrix/federation/v1/version", serverVersion)
	if len(cfg.ServerKeys) > 0 {
		http.HandleFunc("/_matrix/key/v2/server", serverKey)
		//http.HandleFunc("/_matrix/key/v2/query/", queryKey)
	}
	http.HandleFunc("/", notFound)

	fmt.Println("Listening on", cfg.Listen)
	err = http.ListenAndServe(cfg.Listen, nil)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error in HTTP listener:", err)
		os.Exit(10)
	}
}
