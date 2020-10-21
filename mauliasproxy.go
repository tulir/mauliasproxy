package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

type Config struct {
	HomeserverURL string            `yaml:"homeserver_url"`
	Aliases       map[string]string `yaml:"aliases"`
	CacheTTL      int64             `yaml:"cache_ttl"`

	Listen          string `yaml:"listen"`
	ServerWellKnown string `yaml:"server_well_known"`
}

type WellKnownResponse struct {
	ServerName string `json:"m.server"`
}

type ErrorResponse struct {
	Code    string `json:"errcode"`
	Message string `json:"error"`
}

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
		Path:    fmt.Sprintf("/_matrix/client/r0/directory/room/%s", alias),
		RawPath: fmt.Sprintf("/_matrix/client/r0/directory/room/%s", url.PathEscape(alias)),
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

func queryDirectory(w http.ResponseWriter, req *http.Request) {
	alias := req.URL.Query().Get("room_alias")
	target, ok := cfg.Aliases[alias]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Code:    "M_NOT_FOUND",
			Message: fmt.Sprintf("Room alias %s not found", alias),
		})
		return
	}
	resp := resolveAlias(target)
	if !resp.Exists {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Code:    "M_NOT_FOUND",
			Message: fmt.Sprintf("Failed to resolve %s", target),
		})
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func serverWellKnown(w http.ResponseWriter, req *http.Request) {
	_ = json.NewEncoder(w).Encode(WellKnownResponse{ServerName: cfg.ServerWellKnown})
}

func main() {
	data, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to read config.yaml:", err)
		os.Exit(2)
	}
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to parse config.yaml:", err)
		os.Exit(3)
	}
	homeserverURL, err = url.Parse(cfg.HomeserverURL)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to parse homeserver URL:", err)
		os.Exit(4)
	}

	http.HandleFunc("/_matrix/federation/v1/query/directory", queryDirectory)
	http.HandleFunc("/.well-known/matrix/server", serverWellKnown)

	fmt.Println("Listening on", cfg.Listen)
	err = http.ListenAndServe(cfg.Listen, nil)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error in HTTP listener:", err)
		os.Exit(10)
	}
}
