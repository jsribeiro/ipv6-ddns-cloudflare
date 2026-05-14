package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		want      Config
		wantErr   bool
		errMsg    string
		skipWrite bool
	}{
		{
			name: "valid full config",
			content: `
interface: eth0
poll_interval: 60
stability_delay: 10
cloudflare:
  api_token: test-token
  zone_id: test-zone
  record_name: test.example.com
  ttl: 300
  proxied: true
`,
			want: Config{
				Interface:      "eth0",
				PollInterval:   60,
				StabilityDelay: 10,
				CloudFlare: CloudFlareConfig{
					APIToken:   "test-token",
					ZoneID:     "test-zone",
					RecordName: "test.example.com",
					TTL:        300,
					Proxied:    true,
				},
			},
		},
		{
			name: "defaults applied",
			content: `
interface: eth0
cloudflare:
  api_token: test-token
  zone_id: test-zone
  record_name: test.example.com
`,
			want: Config{
				Interface:      "eth0",
				PollInterval:   30,
				StabilityDelay: 5,
				CloudFlare: CloudFlareConfig{
					APIToken:   "test-token",
					ZoneID:     "test-zone",
					RecordName: "test.example.com",
					TTL:        1,
					Proxied:    false,
				},
			},
		},
		{
			name:      "missing file",
			content:   "",
			wantErr:   true,
			errMsg:    "reading config file",
			skipWrite: true,
		},
		{
			name:      "invalid yaml",
			content:   "{invalid",
			wantErr:   true,
			errMsg:    "parsing config file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.skipWrite {
				path = filepath.Join(t.TempDir(), "nonexistent.yaml")
			} else {
				path = filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
					t.Fatal(err)
				}
			}

			got, err := loadConfig(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errMsg)
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("loadConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: Config{
				Interface: "eth0",
				CloudFlare: CloudFlareConfig{
					APIToken:   "token",
					ZoneID:     "zone",
					RecordName: "example.com",
				},
			},
		},
		{
			name: "missing interface",
			config: Config{
				CloudFlare: CloudFlareConfig{
					APIToken:   "token",
					ZoneID:     "zone",
					RecordName: "example.com",
				},
			},
			wantErr: true,
			errMsg:  "interface is required",
		},
		{
			name: "missing api token",
			config: Config{
				Interface: "eth0",
				CloudFlare: CloudFlareConfig{
					ZoneID:     "zone",
					RecordName: "example.com",
				},
			},
			wantErr: true,
			errMsg:  "cloudflare.api_token is required",
		},
		{
			name: "missing zone id",
			config: Config{
				Interface: "eth0",
				CloudFlare: CloudFlareConfig{
					APIToken:   "token",
					RecordName: "example.com",
				},
			},
			wantErr: true,
			errMsg:  "cloudflare.zone_id is required",
		},
		{
			name: "missing record name",
			config: Config{
				Interface: "eth0",
				CloudFlare: CloudFlareConfig{
					APIToken: "token",
					ZoneID:   "zone",
				},
			},
			wantErr: true,
			errMsg:  "cloudflare.record_name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errMsg)
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("expected error %q, got %q", tt.errMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestIsValidPublicIPv6(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"global unicast", "2001:db8::1", true},
		{"another global", "2606:4700:4700::1111", true},
		{"IPv4 mapped", "::ffff:192.0.2.1", false},
		{"IPv4 loopback", "127.0.0.1", false},
		{"link-local", "fe80::1", false},
		{"loopback", "::1", false},
		{"ULA fd00", "fd00::1", false},
		{"ULA fc00", "fc00::1", false},
		{"multicast", "ff02::1", false},
		{"unspecified", "::", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %s", tt.ip)
			}
			got := isValidPublicIPv6(ip)
			if got != tt.want {
				t.Errorf("isValidPublicIPv6(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestGetPublicIPv6(t *testing.T) {
	t.Run("non-existent interface", func(t *testing.T) {
		_, err := getPublicIPv6("nonexistent0")
		if err == nil {
			t.Fatal("expected error for non-existent interface")
		}
		if !strings.Contains(err.Error(), "nonexistent0") {
			t.Errorf("error message should mention interface name, got: %v", err)
		}
	})
}

func TestFetchRecordID(t *testing.T) {
	tests := []struct {
		name           string
		responseStatus int
		responseBody   string
		wantRecordID   string
		wantLastKnown  string
		wantErr        bool
	}{
		{
			name:           "successful fetch",
			responseStatus: http.StatusOK,
			responseBody: `{
				"success": true,
				"result": [{"id": "record-123", "type": "AAAA", "name": "test.example.com", "content": "2001:db8::1", "ttl": 1, "proxied": false}]
			}`,
			wantRecordID:  "record-123",
			wantLastKnown: "2001:db8::1",
		},
		{
			name:           "no records found",
			responseStatus: http.StatusOK,
			responseBody:   `{"success": true, "result": []}`,
			wantRecordID:   "",
			wantLastKnown:  "",
		},
		{
			name:           "api error",
			responseStatus: http.StatusBadRequest,
			responseBody:   `{"success": false, "errors": [{"code": 7003, "message": "invalid zone"}]}`,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("expected GET, got %s", r.Method)
				}
				if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
					t.Errorf("expected Bearer test-token, got %s", auth)
				}
				w.WriteHeader(tt.responseStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			service := &DDNSService{
				config: Config{
					CloudFlare: CloudFlareConfig{
						APIToken:   "test-token",
						ZoneID:     "test-zone",
						RecordName: "test.example.com",
					},
				},
				httpClient: server.Client(),
				apiBaseURL: server.URL,
			}

			err := service.fetchRecordID()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if service.recordID != tt.wantRecordID {
				t.Errorf("recordID = %q, want %q", service.recordID, tt.wantRecordID)
			}
			if service.lastKnownIP != tt.wantLastKnown {
				t.Errorf("lastKnownIP = %q, want %q", service.lastKnownIP, tt.wantLastKnown)
			}
		})
	}
}

func TestUpdateDNS(t *testing.T) {
	tests := []struct {
		name           string
		recordID       string
		responseStatus int
		responseBody   string
		wantErr        bool
		wantRecordID   string
	}{
		{
			name:           "create record",
			recordID:       "",
			responseStatus: http.StatusOK,
			responseBody:   `{"success": true, "result": {"id": "new-record", "type": "AAAA", "name": "test.example.com", "content": "2001:db8::1", "ttl": 1, "proxied": false}}`,
			wantRecordID:   "new-record",
		},
		{
			name:           "update record",
			recordID:       "existing-record",
			responseStatus: http.StatusOK,
			responseBody:   `{"success": true, "result": {"id": "existing-record", "type": "AAAA", "name": "test.example.com", "content": "2001:db8::1", "ttl": 1, "proxied": false}}`,
			wantRecordID:   "existing-record",
		},
		{
			name:           "api error",
			recordID:       "",
			responseStatus: http.StatusBadRequest,
			responseBody:   `{"success": false, "errors": [{"code": 81057, "message": "record already exists"}]}`,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
				}
				if ct := r.Header.Get("Content-Type"); ct != "application/json" {
					t.Errorf("unexpected content-type: %s", ct)
				}

				var reqBody map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					t.Fatalf("failed to decode request body: %v", err)
				}

				if reqBody["type"] != "AAAA" {
					t.Errorf("expected type AAAA, got %v", reqBody["type"])
				}
				if reqBody["content"] != "2001:db8::1" {
					t.Errorf("expected content 2001:db8::1, got %v", reqBody["content"])
				}

				if tt.recordID == "" {
					if r.Method != "POST" {
						t.Errorf("expected POST for create, got %s", r.Method)
					}
				} else {
					if r.Method != "PUT" {
						t.Errorf("expected PUT for update, got %s", r.Method)
					}
				}

				w.WriteHeader(tt.responseStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			service := &DDNSService{
				config: Config{
					CloudFlare: CloudFlareConfig{
						APIToken:   "test-token",
						ZoneID:     "test-zone",
						RecordName: "test.example.com",
						TTL:        1,
						Proxied:    false,
					},
				},
				httpClient: server.Client(),
				recordID:   tt.recordID,
				apiBaseURL: server.URL,
			}

			err := service.updateDNS("2001:db8::1")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if service.recordID != tt.wantRecordID {
				t.Errorf("recordID = %q, want %q", service.recordID, tt.wantRecordID)
			}
		})
	}
}

func TestCheckAndUpdate(t *testing.T) {
	t.Run("no change from last known", func(t *testing.T) {
		service := &DDNSService{
			config:      Config{Interface: "eth0"},
			lastKnownIP: "2001:db8::1",
			getIPv6: func(string) (string, error) {
				return "2001:db8::1", nil
			},
		}

		service.checkAndUpdate()

		if service.pendingIP != "" {
			t.Errorf("pendingIP should be empty, got %q", service.pendingIP)
		}
	})

	t.Run("new IP detected", func(t *testing.T) {
		service := &DDNSService{
			config: Config{
				Interface:      "eth0",
				StabilityDelay: 1,
			},
			lastKnownIP: "2001:db8::1",
			getIPv6: func(string) (string, error) {
				return "2001:db8::2", nil
			},
		}

		service.checkAndUpdate()

		if service.pendingIP != "2001:db8::2" {
			t.Errorf("pendingIP = %q, want %q", service.pendingIP, "2001:db8::2")
		}
		if service.stabilityTimer == nil {
			t.Error("stabilityTimer should be set")
		}
		service.cancelPendingUpdate()
	})

	t.Run("IP reverted to last known", func(t *testing.T) {
		service := &DDNSService{
			config:      Config{Interface: "eth0"},
			lastKnownIP: "2001:db8::1",
			pendingIP:   "2001:db8::2",
			getIPv6: func(string) (string, error) {
				return "2001:db8::1", nil
			},
		}

		service.checkAndUpdate()

		if service.pendingIP != "" {
			t.Errorf("pendingIP should be cleared, got %q", service.pendingIP)
		}
	})

	t.Run("error getting IPv6", func(t *testing.T) {
		service := &DDNSService{
			config: Config{Interface: "eth0"},
			getIPv6: func(string) (string, error) {
				return "", fmt.Errorf("network down")
			},
		}

		service.checkAndUpdate()

		if service.pendingIP != "" {
			t.Errorf("pendingIP should be empty after error, got %q", service.pendingIP)
		}
	})
}

func TestStartStabilityTimer(t *testing.T) {
	t.Run("stable IP updates DNS", func(t *testing.T) {
		updated := make(chan string, 1)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PUT" {
				var body map[string]interface{}
				json.NewDecoder(r.Body).Decode(&body)
				if content, ok := body["content"].(string); ok {
					updated <- content
				}
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success": true, "result": {"id": "rec-1"}}`))
		}))
		defer server.Close()

		service := &DDNSService{
			config: Config{
				Interface:      "eth0",
				StabilityDelay: 1,
				CloudFlare: CloudFlareConfig{
					APIToken:   "token",
					ZoneID:     "zone",
					RecordName: "test.example.com",
				},
			},
			httpClient: server.Client(),
			recordID:   "rec-1",
			getIPv6: func(string) (string, error) {
				return "2001:db8::5", nil
			},
			apiBaseURL: server.URL,
		}

		service.checkAndUpdate()

		select {
		case ip := <-updated:
			if ip != "2001:db8::5" {
				t.Errorf("updated IP = %q, want %q", ip, "2001:db8::5")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for DNS update")
		}

		// Wait for the async timer goroutine to finish updating state
		deadline := time.Now().Add(2 * time.Second)
		for {
			service.mu.Lock()
			lastKnown := service.lastKnownIP
			service.mu.Unlock()
			if lastKnown == "2001:db8::5" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("lastKnownIP not updated in time: got %q", lastKnown)
			}
			time.Sleep(10 * time.Millisecond)
		}
		service.mu.Lock()
		pending := service.pendingIP
		service.mu.Unlock()
		if pending != "" {
			t.Errorf("pendingIP should be cleared, got %q", pending)
		}
	})
}

func TestCancelPendingUpdate(t *testing.T) {
	service := &DDNSService{
		config: Config{
			StabilityDelay: 60,
		},
		pendingIP: "2001:db8::1",
	}
	service.startStabilityTimer()

	if service.stabilityTimer == nil {
		t.Fatal("stabilityTimer should be set")
	}

	service.cancelPendingUpdate()

	if service.stabilityTimer != nil {
		t.Error("stabilityTimer should be nil after cancel")
	}
	if service.pendingIP != "" {
		t.Errorf("pendingIP should be empty, got %q", service.pendingIP)
	}
}
