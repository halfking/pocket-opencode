# OpenCode Instance Manager

Lightweight daemon service for managing OpenCode instances remotely.

## Features

- 🚀 **Remote Start/Stop**: Control OpenCode instances remotely
- 💓 **Health Monitoring**: Automatic health checks
- 🔄 **Auto-Restart**: Restart failed instances
- 📡 **WebSocket Communication**: Real-time bidirectional communication
- 🔧 **Auto-Recovery**: Automatic reconnection and recovery
- 🖥️ **Cross-Platform**: macOS, Linux, Windows support

## Installation

### Quick Install (Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/your-org/opencode-manager/main/install.sh | bash
```

### Manual Install

1. Download the binary for your platform:

```bash
# macOS (ARM64)
curl -L https://github.com/your-org/opencode-manager/releases/latest/download/opencode-manager-darwin-arm64 \
  -o /usr/local/bin/opencode-manager

# macOS (Intel)
curl -L https://github.com/your-org/opencode-manager/releases/latest/download/opencode-manager-darwin-amd64 \
  -o /usr/local/bin/opencode-manager

# Linux (ARM64)
curl -L https://github.com/your-org/opencode-manager/releases/latest/download/opencode-manager-linux-arm64 \
  -o /usr/local/bin/opencode-manager

# Linux (AMD64)
curl -L https://github.com/your-org/opencode-manager/releases/latest/download/opencode-manager-linux-amd64 \
  -o /usr/local/bin/opencode-manager
```

2. Make it executable:

```bash
chmod +x /usr/local/bin/opencode-manager
```

3. Create configuration:

```bash
sudo mkdir -p /etc/opencode-instance-manager
sudo cat > /etc/opencode-instance-manager/config.json << 'EOF'
{
  "backendURL": "wss://pocket.your-domain.com",
  "instanceID": "my-instance",
  "opencodePath": "/path/to/opencode",
  "autoStart": true,
  "port": 4096,
  "authToken": "your-auth-token",
  "healthCheck": {
    "interval": 30,
    "timeout": 5
  }
}
EOF
```

4. Install as system service (see below)

## Configuration

Example `config.json`:

```json
{
  "backendURL": "wss://pocket.your-domain.com",
  "instanceID": "dev-macbook-pro",
  "opencodePath": "/Users/username/workspace/ai/opencode",
  "autoStart": true,
  "port": 4096,
  "authToken": "your-instance-auth-token",
  "healthCheck": {
    "interval": 30,
    "timeout": 5
  }
}
```

### Configuration Fields

- `backendURL`: Pocket Backend WebSocket URL
- `instanceID`: Unique identifier for this instance
- `opencodePath`: Path to OpenCode installation
- `autoStart`: Start OpenCode automatically on manager start
- `port`: OpenCode API port (default: 4096)
- `authToken`: Authentication token
- `healthCheck.interval`: Health check interval in seconds
- `healthCheck.timeout`: Health check timeout in seconds

## System Service Installation

### macOS (launchd)

```bash
# Create service file
cat > ~/Library/LaunchAgents/com.opencode.manager.plist << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.opencode.manager</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/opencode-manager</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/opencode-manager.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/opencode-manager.error.log</string>
</dict>
</plist>
EOF

# Load service
launchctl load ~/Library/LaunchAgents/com.opencode.manager.plist

# Start service
launchctl start com.opencode.manager

# Check status
launchctl list | grep opencode
```

### Linux (systemd)

```bash
# Create service file
sudo cat > /etc/systemd/system/opencode-manager.service << 'EOF'
[Unit]
Description=OpenCode Instance Manager
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/opencode-manager
Restart=always
RestartSec=5
User=your-username
Environment="OPENCODE_MANAGER_CONFIG=/etc/opencode-instance-manager/config.json"

[Install]
WantedBy=multi-user.target
EOF

# Reload systemd
sudo systemctl daemon-reload

# Enable service
sudo systemctl enable opencode-manager

# Start service
sudo systemctl start opencode-manager

# Check status
sudo systemctl status opencode-manager

# View logs
sudo journalctl -u opencode-manager -f
```

## Usage

### Manual Run

```bash
# Run with default config
opencode-manager

# Run with custom config
OPENCODE_MANAGER_CONFIG=/path/to/config.json opencode-manager
```

### Service Management

#### macOS

```bash
# Start
launchctl start com.opencode.manager

# Stop
launchctl stop com.opencode.manager

# Restart
launchctl stop com.opencode.manager && launchctl start com.opencode.manager

# View logs
tail -f /tmp/opencode-manager.log
```

#### Linux

```bash
# Start
sudo systemctl start opencode-manager

# Stop
sudo systemctl stop opencode-manager

# Restart
sudo systemctl restart opencode-manager

# Status
sudo systemctl status opencode-manager

# Logs
sudo journalctl -u opencode-manager -f
```

## Remote Control

The manager responds to these commands from Pocket Backend:

- `command.start`: Start OpenCode
- `command.stop`: Stop OpenCode
- `command.restart`: Restart OpenCode
- `command.status`: Report current status

## Building from Source

```bash
# Clone repository
git clone https://github.com/your-org/opencode-manager
cd opencode-manager

# Build
go build -o opencode-manager main.go

# Build for multiple platforms
GOOS=darwin GOARCH=arm64 go build -o opencode-manager-darwin-arm64 main.go
GOOS=darwin GOARCH=amd64 go build -o opencode-manager-darwin-amd64 main.go
GOOS=linux GOARCH=arm64 go build -o opencode-manager-linux-arm64 main.go
GOOS=linux GOARCH=amd64 go build -o opencode-manager-linux-amd64 main.go
GOOS=windows GOARCH=amd64 go build -o opencode-manager-windows-amd64.exe main.go
```

## Troubleshooting

### Manager not starting

Check logs:
```bash
# macOS
tail -f /tmp/opencode-manager.log

# Linux
sudo journalctl -u opencode-manager -n 50
```

### OpenCode not starting

1. Check OpenCode path in config
2. Verify `bun` is installed and in PATH
3. Check OpenCode directory permissions

### Connection issues

1. Verify `backendURL` in config
2. Check auth token
3. Ensure network connectivity

### Health check failing

1. Verify OpenCode port in config
2. Check if OpenCode API is responding:
   ```bash
   curl http://localhost:4096/api/health
   ```

## Uninstallation

### macOS

```bash
# Stop and unload service
launchctl stop com.opencode.manager
launchctl unload ~/Library/LaunchAgents/com.opencode.manager.plist

# Remove files
rm ~/Library/LaunchAgents/com.opencode.manager.plist
rm /usr/local/bin/opencode-manager
sudo rm -rf /etc/opencode-instance-manager
```

### Linux

```bash
# Stop and disable service
sudo systemctl stop opencode-manager
sudo systemctl disable opencode-manager

# Remove files
sudo rm /etc/systemd/system/opencode-manager.service
sudo rm /usr/local/bin/opencode-manager
sudo rm -rf /etc/opencode-instance-manager

# Reload systemd
sudo systemctl daemon-reload
```

## License

MIT
