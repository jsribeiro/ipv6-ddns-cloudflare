// ipv6-ddns-cloudflare - IPv6 Dynamic DNS updater for CloudFlare
// Copyright (C) 2025 João Sena Ribeiro <sena@smux.net>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Interface      string           `yaml:"interface"`
	PollInterval   int              `yaml:"poll_interval"`
	StabilityDelay int              `yaml:"stability_delay"`
	CloudFlare     CloudFlareConfig `yaml:"cloudflare"`
}

type CloudFlareConfig struct {
	APIToken   string `yaml:"api_token"`
	ZoneID     string `yaml:"zone_id"`
	RecordName string `yaml:"record_name"`
	TTL        int    `yaml:"ttl"`
	Proxied    bool   `yaml:"proxied"`
}

type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type CloudFlareResponse struct {
	Success bool        `json:"success"`
	Errors  []CFError   `json:"errors"`
	Result  interface{} `json:"result"`
}

type CFError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func isValidPublicIPv6(ip net.IP) bool {
	return ip.To4() == nil && ip.IsGlobalUnicast() && !ip.IsPrivate()
}

type DDNSService struct {
	config         Config
	httpClient     *http.Client
	lastKnownIP    string
	pendingIP      string
	stabilityTimer *time.Timer
	recordID       string
	getIPv6        func(string) (string, error)
	apiBaseURL     string
	mu             sync.Mutex
}

func main() {
	configPath := flag.String("config", "/etc/ipv6-ddns-cloudflare/config.yaml", "Path to configuration file")
	flag.Parse()

	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if err := validateConfig(config); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	service := &DDNSService{
		config: config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		getIPv6:    getPublicIPv6,
		apiBaseURL: "https://api.cloudflare.com/client/v4",
	}

	// Get the current DNS record ID
	if err := service.fetchRecordID(); err != nil {
		log.Fatalf("Failed to fetch DNS record: %v", err)
	}

	log.Printf("Starting IPv6 DDNS service for interface %s, updating %s",
		config.Interface, config.CloudFlare.RecordName)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(time.Duration(config.PollInterval) * time.Second)
	defer ticker.Stop()

	// Initial check
	service.checkAndUpdate()

	for {
		select {
		case <-ticker.C:
			service.checkAndUpdate()
		case <-sigChan:
			log.Println("Shutting down...")
			if service.stabilityTimer != nil {
				service.stabilityTimer.Stop()
			}
			return
		}
	}
}

func loadConfig(path string) (Config, error) {
	var config Config

	data, err := os.ReadFile(path)
	if err != nil {
		return config, fmt.Errorf("reading config file: %w", err)
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("parsing config file: %w", err)
	}

	// Set defaults
	if config.PollInterval == 0 {
		config.PollInterval = 30
	}
	if config.StabilityDelay == 0 {
		config.StabilityDelay = 5
	}
	if config.CloudFlare.TTL == 0 {
		config.CloudFlare.TTL = 1 // Auto
	}

	return config, nil
}

func validateConfig(config Config) error {
	if config.Interface == "" {
		return fmt.Errorf("interface is required")
	}
	if config.CloudFlare.APIToken == "" {
		return fmt.Errorf("cloudflare.api_token is required")
	}
	if config.CloudFlare.ZoneID == "" {
		return fmt.Errorf("cloudflare.zone_id is required")
	}
	if config.CloudFlare.RecordName == "" {
		return fmt.Errorf("cloudflare.record_name is required")
	}
	return nil
}

func getPublicIPv6(ifaceName string) (string, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", fmt.Errorf("interface %s not found: %w", ifaceName, err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("getting addresses for %s: %w", ifaceName, err)
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		ip := ipNet.IP

		if isValidPublicIPv6(ip) {
			return ip.String(), nil
		}
	}

	return "", fmt.Errorf("no public IPv6 address found on interface %s", ifaceName)
}

func (s *DDNSService) checkAndUpdate() {
	currentIP, err := s.getIPv6(s.config.Interface)
	if err != nil {
		log.Printf("Error getting IPv6 address: %v", err)
		return
	}

	s.mu.Lock()
	// No change from last known stable IP
	if currentIP == s.lastKnownIP {
		// If we had a pending change that reverted, cancel it
		if s.pendingIP != "" && s.pendingIP != currentIP {
			log.Printf("Address reverted to %s, cancelling pending update", currentIP)
			s.cancelPendingUpdateLocked()
		}
		s.mu.Unlock()
		return
	}

	// New IP detected
	if currentIP != s.pendingIP {
		if s.lastKnownIP == "" {
			log.Printf("Detected IPv6 address: %s", currentIP)
		} else {
			log.Printf("Detected new IPv6 address: %s (was: %s)", currentIP, s.lastKnownIP)
		}
		s.pendingIP = currentIP
		s.startStabilityTimerLocked()
	}
	s.mu.Unlock()
}

func (s *DDNSService) startStabilityTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startStabilityTimerLocked()
}

func (s *DDNSService) startStabilityTimerLocked() {
	// Cancel any existing timer
	if s.stabilityTimer != nil {
		s.stabilityTimer.Stop()
	}

	log.Printf("Waiting %d seconds for address stability...", s.config.StabilityDelay)

	s.stabilityTimer = time.AfterFunc(time.Duration(s.config.StabilityDelay)*time.Second, func() {
		s.mu.Lock()

		// Verify the address is still the same
		currentIP, err := s.getIPv6(s.config.Interface)
		if err != nil {
			log.Printf("Error verifying IPv6 address: %v", err)
			s.pendingIP = ""
			s.mu.Unlock()
			return
		}

		if currentIP != s.pendingIP {
			log.Printf("Address changed during stability window, restarting timer")
			s.pendingIP = currentIP
			s.startStabilityTimerLocked()
			s.mu.Unlock()
			return
		}

		// Address is stable, update DNS
		log.Printf("Address stable for %d seconds, updating DNS", s.config.StabilityDelay)
		s.mu.Unlock()
		err = s.updateDNS(currentIP)
		s.mu.Lock()
		if err != nil {
			log.Printf("Failed to update DNS: %v", err)
		} else {
			log.Printf("Successfully updated DNS record to %s", currentIP)
			s.lastKnownIP = currentIP
		}
		s.pendingIP = ""
		s.mu.Unlock()
	})
}

func (s *DDNSService) cancelPendingUpdate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelPendingUpdateLocked()
}

func (s *DDNSService) cancelPendingUpdateLocked() {
	if s.stabilityTimer != nil {
		s.stabilityTimer.Stop()
		s.stabilityTimer = nil
	}
	s.pendingIP = ""
}

func (s *DDNSService) fetchRecordID() error {
	cfConfig := s.config.CloudFlare
	url := fmt.Sprintf("%s/zones/%s/dns_records?type=AAAA&name=%s",
		s.apiBaseURL, cfConfig.ZoneID, cfConfig.RecordName)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+cfConfig.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	var cfResp struct {
		Success bool        `json:"success"`
		Errors  []CFError   `json:"errors"`
		Result  []DNSRecord `json:"result"`
	}

	if err := json.Unmarshal(body, &cfResp); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	if !cfResp.Success {
		return fmt.Errorf("CloudFlare API error: %v", cfResp.Errors)
	}

	if len(cfResp.Result) == 0 {
		// Record doesn't exist, we'll create it on first update
		log.Printf("DNS record %s does not exist, will create on first update", cfConfig.RecordName)
		return nil
	}

	s.mu.Lock()
	s.recordID = cfResp.Result[0].ID
	s.lastKnownIP = cfResp.Result[0].Content
	s.mu.Unlock()

	log.Printf("Found existing record %s with IP %s", cfConfig.RecordName, cfResp.Result[0].Content)

	return nil
}

func (s *DDNSService) updateDNS(ip string) error {
	s.mu.Lock()
	recordID := s.recordID
	cfConfig := s.config.CloudFlare
	s.mu.Unlock()

	record := map[string]interface{}{
		"type":    "AAAA",
		"name":    cfConfig.RecordName,
		"content": ip,
		"ttl":     cfConfig.TTL,
		"proxied": cfConfig.Proxied,
	}

	body, err := json.Marshal(record)
	if err != nil {
		return err
	}

	var url string
	var method string

	if recordID == "" {
		// Create new record
		url = fmt.Sprintf("%s/zones/%s/dns_records",
			s.apiBaseURL, cfConfig.ZoneID)
		method = "POST"
	} else {
		// Update existing record
		url = fmt.Sprintf("%s/zones/%s/dns_records/%s",
			s.apiBaseURL, cfConfig.ZoneID, recordID)
		method = "PUT"
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+cfConfig.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	var cfResp struct {
		Success bool      `json:"success"`
		Errors  []CFError `json:"errors"`
		Result  DNSRecord `json:"result"`
	}

	if err := json.Unmarshal(respBody, &cfResp); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	if !cfResp.Success {
		var errMsgs []string
		for _, e := range cfResp.Errors {
			errMsgs = append(errMsgs, e.Message)
		}
		return fmt.Errorf("CloudFlare API error: %s", strings.Join(errMsgs, ", "))
	}

	// Store the record ID if this was a create
	s.mu.Lock()
	if s.recordID == "" {
		s.recordID = cfResp.Result.ID
	}
	s.mu.Unlock()

	return nil
}
