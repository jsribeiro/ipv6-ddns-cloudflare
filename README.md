# IPv6 DDNS CloudFlare

A lightweight Go service that monitors an IPv6 address on a network interface and updates a CloudFlare DNS record when it changes.

## Features

- Monitors a specific network interface for IPv6 address changes
- Filters out link-local, loopback, and ULA addresses automatically
- 5-second stability delay to avoid updating during network churn
- Creates the DNS record if it doesn't exist
- Runs as a systemd service with security hardening
- Minimal dependencies (just the Go standard library + YAML parser)

## Quick Start

1. **Build:**
   ```bash
   go build -ldflags="-s -w" -o ipv6-ddns-cloudflare .
   ```
   
   Or cross-compile from another platform:
   ```bash
   GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ipv6-ddns-cloudflare .
   ```

2. **Install:**
   ```bash
   chmod +x install.sh
   ./install.sh
   ```

2. **Configure:**
   ```bash
   sudo nano /etc/ipv6-ddns-cloudflare/config.yaml
   ```
   
   Set your:
   - `interface`: Network interface name (e.g., `eth0`, `enp1s0`)
   - `cloudflare.api_token`: CloudFlare API token with Zone.DNS edit permissions
   - `cloudflare.zone_id`: Your zone ID (found in CloudFlare dashboard)
   - `cloudflare.record_name`: The FQDN to update (e.g., `home.example.com`)

3. **Start the service:**
   ```bash
   sudo systemctl enable --now ipv6-ddns-cloudflare
   ```

4. **Check logs:**
   ```bash
   sudo journalctl -u ipv6-ddns-cloudflare -f
   ```

## Getting CloudFlare Credentials

### API Token
1. Go to https://dash.cloudflare.com/profile/api-tokens
2. Create Token → Use "Edit zone DNS" template
3. Set Zone Resources to your domain
4. Copy the token

### Zone ID
1. Go to your domain in CloudFlare dashboard
2. Scroll down on the Overview page
3. Zone ID is in the API section at the bottom

## Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `interface` | (required) | Network interface to monitor |
| `poll_interval` | `30` | Seconds between checks |
| `stability_delay` | `5` | Seconds to wait before updating after a change |
| `cloudflare.api_token` | (required) | CloudFlare API token |
| `cloudflare.zone_id` | (required) | CloudFlare Zone ID |
| `cloudflare.record_name` | (required) | DNS record name (FQDN) |
| `cloudflare.ttl` | `1` | TTL in seconds (1 = automatic) |
| `cloudflare.proxied` | `false` | Enable CloudFlare proxy |

## Running Manually

```bash
./ipv6-ddns-cloudflare -config config.yaml
```

## Author

João Sena Ribeiro <sena@smux.net>

## License

GPL-3.0 - See [LICENSE](LICENSE) for details.
