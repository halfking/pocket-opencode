#!/bin/bash
# OpenCode Instance Manager - Quick Install Script

set -e

VERSION="${VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="/etc/opencode-instance-manager"

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "=========================================="
echo "OpenCode Instance Manager Installer"
echo "=========================================="
echo ""

# Detect OS and architecture
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
    Darwin)
        OS="darwin"
        ;;
    Linux)
        OS="linux"
        ;;
    *)
        echo -e "${RED}Unsupported OS: $OS${NC}"
        exit 1
        ;;
esac

case "$ARCH" in
    x86_64)
        ARCH="amd64"
        ;;
    arm64|aarch64)
        ARCH="arm64"
        ;;
    *)
        echo -e "${RED}Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

BINARY_NAME="opencode-manager-${OS}-${ARCH}"
DOWNLOAD_URL="https://github.com/your-org/opencode-manager/releases/${VERSION}/download/${BINARY_NAME}"

echo "Detected: $OS $ARCH"
echo ""

# Download binary
echo "Downloading OpenCode Instance Manager..."
if command -v curl >/dev/null 2>&1; then
    curl -L "$DOWNLOAD_URL" -o /tmp/opencode-manager
elif command -v wget >/dev/null 2>&1; then
    wget "$DOWNLOAD_URL" -O /tmp/opencode-manager
else
    echo -e "${RED}Error: curl or wget required${NC}"
    exit 1
fi

# Make executable
chmod +x /tmp/opencode-manager

# Install
echo "Installing to $INSTALL_DIR..."
if [ -w "$INSTALL_DIR" ]; then
    mv /tmp/opencode-manager "$INSTALL_DIR/opencode-manager"
else
    sudo mv /tmp/opencode-manager "$INSTALL_DIR/opencode-manager"
fi

echo -e "${GREEN}✓ Binary installed${NC}"

# Create config directory
echo "Creating configuration directory..."
if [ -w "$(dirname "$CONFIG_DIR")" ]; then
    mkdir -p "$CONFIG_DIR"
else
    sudo mkdir -p "$CONFIG_DIR"
fi

# Generate config file if not exists
if [ ! -f "$CONFIG_DIR/config.json" ]; then
    echo "Generating default configuration..."
    
    # Prompt for configuration
    read -p "Backend URL (wss://pocket.your-domain.com): " BACKEND_URL
    BACKEND_URL=${BACKEND_URL:-wss://pocket.your-domain.com}
    
    read -p "Instance ID ($(hostname)): " INSTANCE_ID
    INSTANCE_ID=${INSTANCE_ID:-$(hostname)}
    
    read -p "OpenCode path ($HOME/workspace/ai/opencode): " OPENCODE_PATH
    OPENCODE_PATH=${OPENCODE_PATH:-$HOME/workspace/ai/opencode}
    
    read -p "Auto-start OpenCode? (y/n): " AUTO_START
    if [ "$AUTO_START" = "y" ]; then
        AUTO_START="true"
    else
        AUTO_START="false"
    fi
    
    read -p "Auth token: " AUTH_TOKEN
    
    # Create config
    CONFIG_CONTENT=$(cat <<EOF
{
  "backendURL": "$BACKEND_URL",
  "instanceID": "$INSTANCE_ID",
  "opencodePath": "$OPENCODE_PATH",
  "autoStart": $AUTO_START,
  "port": 4096,
  "authToken": "$AUTH_TOKEN",
  "healthCheck": {
    "interval": 30,
    "timeout": 5
  }
}
EOF
)
    
    if [ -w "$CONFIG_DIR" ]; then
        echo "$CONFIG_CONTENT" > "$CONFIG_DIR/config.json"
    else
        echo "$CONFIG_CONTENT" | sudo tee "$CONFIG_DIR/config.json" > /dev/null
    fi
    
    echo -e "${GREEN}✓ Configuration created${NC}"
else
    echo -e "${YELLOW}Configuration already exists, skipping${NC}"
fi

# Install as system service
echo ""
read -p "Install as system service? (y/n): " INSTALL_SERVICE

if [ "$INSTALL_SERVICE" = "y" ]; then
    if [ "$OS" = "darwin" ]; then
        # macOS launchd
        echo "Installing macOS service..."
        
        PLIST_FILE="$HOME/Library/LaunchAgents/com.opencode.manager.plist"
        
        cat > "$PLIST_FILE" <<'EOF'
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
        
        launchctl load "$PLIST_FILE"
        launchctl start com.opencode.manager
        
        echo -e "${GREEN}✓ Service installed and started${NC}"
        echo ""
        echo "Service management:"
        echo "  Start:   launchctl start com.opencode.manager"
        echo "  Stop:    launchctl stop com.opencode.manager"
        echo "  Logs:    tail -f /tmp/opencode-manager.log"
        
    elif [ "$OS" = "linux" ]; then
        # Linux systemd
        echo "Installing systemd service..."
        
        SERVICE_FILE="/etc/systemd/system/opencode-manager.service"
        
        sudo tee "$SERVICE_FILE" > /dev/null <<EOF
[Unit]
Description=OpenCode Instance Manager
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/opencode-manager
Restart=always
RestartSec=5
User=$USER
Environment="OPENCODE_MANAGER_CONFIG=/etc/opencode-instance-manager/config.json"

[Install]
WantedBy=multi-user.target
EOF
        
        sudo systemctl daemon-reload
        sudo systemctl enable opencode-manager
        sudo systemctl start opencode-manager
        
        echo -e "${GREEN}✓ Service installed and started${NC}"
        echo ""
        echo "Service management:"
        echo "  Start:   sudo systemctl start opencode-manager"
        echo "  Stop:    sudo systemctl stop opencode-manager"
        echo "  Status:  sudo systemctl status opencode-manager"
        echo "  Logs:    sudo journalctl -u opencode-manager -f"
    fi
fi

echo ""
echo "=========================================="
echo -e "${GREEN}Installation Complete!${NC}"
echo "=========================================="
echo ""
echo "Configuration: $CONFIG_DIR/config.json"
echo ""
echo "To run manually:"
echo "  opencode-manager"
echo ""
echo "To test:"
echo "  $INSTALL_DIR/opencode-manager --version"
echo ""
