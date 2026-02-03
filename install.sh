#!/bin/bash
# ipv6-ddns-cloudflare - IPv6 Dynamic DNS updater for CloudFlare
# Copyright (C) 2025 João Sena Ribeiro <sena@smux.net>
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
# GNU General Public License for more details.
#
# You should have received a copy of the GNU General Public License
# along with this program. If not, see <https://www.gnu.org/licenses/>.

set -e

# Install script for ipv6-ddns-cloudflare

echo "Installing binary to /usr/local/sbin..."
sudo install -m 755 ipv6-ddns-cloudflare /usr/local/sbin/

echo "Creating config directory..."
sudo mkdir -p /etc/ipv6-ddns-cloudflare

if [ ! -f /etc/ipv6-ddns-cloudflare/config.yaml ]; then
    echo "Installing example config..."
    sudo install -m 600 config.example.yaml /etc/ipv6-ddns-cloudflare/config.yaml
    echo ""
    echo "IMPORTANT: Edit /etc/ipv6-ddns-cloudflare/config.yaml with your settings!"
else
    echo "Config already exists, not overwriting."
fi

echo "Installing systemd service..."
sudo install -m 644 ipv6-ddns-cloudflare.service /etc/systemd/system/

echo "Reloading systemd..."
sudo systemctl daemon-reload

echo "Enabling service..."
sudo systemctl enable ipv6-ddns-cloudflare

echo ""
echo "Installation complete!"
echo ""
echo "Next steps:"
echo "  1. Edit the config: sudo nano /etc/ipv6-ddns-cloudflare/config.yaml"
echo "  2. Start the service: sudo systemctl start ipv6-ddns-cloudflare"
echo "  3. Check status: sudo systemctl status ipv6-ddns-cloudflare"
echo "  4. View logs: sudo journalctl -u ipv6-ddns-cloudflare -f"
