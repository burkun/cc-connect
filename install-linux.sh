#!/bin/bash
#
# cc-connect Linux Installation Script
# Installs cc-connect as a systemd daemon service
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/chenhg5/cc-connect/main/install-linux.sh | bash
#   or
#   ./install-linux.sh
#
# Options:
#   --skip-build       Skip building from source (use if binary exists)
#   --beta             Install beta version (download from releases)
#   --no-daemon        Don't install as daemon, just build binary
#   --uninstall        Uninstall cc-connect
#

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default settings
INSTALL_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/.cc-connect"
DATA_DIR="$HOME/.cc-connect"
LOG_FILE="$HOME/.cc-connect/cc-connect.log"
REPO_URL="https://github.com/chenhg5/cc-connect.git"
SERVICE_NAME="cc-connect"

# Parse arguments
SKIP_BUILD=false
INSTALL_BETA=false
NO_DAEMON=false
UNINSTALL_MODE=false

for arg in "$@"; do
    case $arg in
        --skip-build) SKIP_BUILD=true ;;
        --beta) INSTALL_BETA=true ;;
        --no-daemon) NO_DAEMON=true ;;
        --uninstall) UNINSTALL_MODE=true ;;
        --help)
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --skip-build    Skip building from source (use if binary exists)"
            echo "  --beta          Install beta version (download from releases)"
            echo "  --no-daemon     Don't install as daemon, just build binary"
            echo "  --uninstall     Uninstall cc-connect"
            echo "  --help          Show this help message"
            exit 0
            ;;
    esac
done

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

ensure_path() {
    # Add INSTALL_DIR to PATH if not already present
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        log_info "Adding $INSTALL_DIR to PATH..."

        # Update PATH for current session
        export PATH="$INSTALL_DIR:$PATH"

        # Persist to shell config
        local SHELL_RC=""
        if [[ -f "$HOME/.bashrc" ]]; then
            SHELL_RC="$HOME/.bashrc"
        elif [[ -f "$HOME/.zshrc" ]]; then
            SHELL_RC="$HOME/.zshrc"
        elif [[ -f "$HOME/.profile" ]]; then
            SHELL_RC="$HOME/.profile"
        fi

        if [[ -n "$SHELL_RC" ]]; then
            # Check if already in shell config
            if ! grep -q "export PATH=\"\$HOME/.local/bin:\$PATH\"" "$SHELL_RC" 2>/dev/null; then
                echo "" >> "$SHELL_RC"
                echo "# Added by cc-connect installer" >> "$SHELL_RC"
                echo "export PATH=\"\$HOME/.local/bin:\$PATH\"" >> "$SHELL_RC"
                log_info "Added to $SHELL_RC - run 'source $SHELL_RC' or open a new terminal"
            fi
        fi
    fi
}

check_root() {
    # No longer needed - everything installs to user directories
    true
}

uninstall() {
    log_info "Uninstalling cc-connect..."

    # Stop and disable service
    if command -v systemctl &> /dev/null; then
        if systemctl --user is-active cc-connect &> /dev/null; then
            log_info "Stopping user-level systemd service..."
            systemctl --user stop cc-connect
            systemctl --user disable cc-connect
        fi
    fi

    # Remove service files
    if [[ -f "$HOME/.config/systemd/user/cc-connect.service" ]]; then
        rm -f "$HOME/.config/systemd/user/cc-connect.service"
        systemctl --user daemon-reload 2>/dev/null || true
    fi

    # Remove binary
    if [[ -f "$INSTALL_DIR/cc-connect" ]]; then
        rm -f "$INSTALL_DIR/cc-connect"
        log_success "Binary removed from $INSTALL_DIR"
    fi

    # Ask about config
    if [[ -d "$CONFIG_DIR" ]]; then
        read -p "Remove configuration directory $CONFIG_DIR? [y/N] " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            rm -rf "$CONFIG_DIR"
            log_success "Configuration directory removed"
        fi
    fi

    log_success "cc-connect uninstalled successfully"
    exit 0
}

check_dependencies() {
    log_info "Checking dependencies..."

    local missing=()

    # Check Go (only needed for building from source)
    if [[ "$SKIP_BUILD" == "false" ]] && [[ "$INSTALL_BETA" == "false" ]]; then
        if ! command -v go &> /dev/null; then
            missing+=("go (Go 1.22+)")
        else
            GO_VERSION=$(go version | grep -oP 'go\d+\.\d+' | head -1)
            log_info "Found Go: $GO_VERSION"
        fi
    fi

    # Check git
    if [[ "$SKIP_BUILD" == "false" ]] && [[ "$INSTALL_BETA" == "false" ]]; then
        if ! command -v git &> /dev/null; then
            missing+=("git")
        fi
    fi

    # Check make
    if [[ "$SKIP_BUILD" == "false" ]] && [[ "$INSTALL_BETA" == "false" ]]; then
        if ! command -v make &> /dev/null; then
            missing+=("make")
        fi
    fi

    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing dependencies: ${missing[*]}"
        echo ""
        echo "Install them with:"
        echo "  Ubuntu/Debian: sudo apt install -y golang-go git make"
        echo "  CentOS/RHEL:   sudo yum install -y golang git make"
        echo "  Arch Linux:    sudo pacman -S go git make"
        echo "  Fedora:        sudo dnf install -y golang git make"
        echo ""
        echo "For Go, you may need to install a newer version from:"
        echo "  https://go.dev/doc/install"
        exit 1
    fi

    log_success "All dependencies satisfied"
}

install_from_source() {
    log_info "Building cc-connect from source..."

    local TMP_DIR
    TMP_DIR=$(mktemp -d)

    cd "$TMP_DIR"

    # Clone repository
    log_info "Cloning repository..."
    git clone --depth 1 "$REPO_URL" cc-connect
    cd cc-connect

    # Build
    log_info "Compiling..."
    make build

    # Install binary
    log_info "Installing binary to $INSTALL_DIR..."
    mkdir -p "$INSTALL_DIR"
    cp cc-connect "$INSTALL_DIR/cc-connect"
    chmod +x "$INSTALL_DIR/cc-connect"

    # Ensure PATH includes INSTALL_DIR
    ensure_path

    # Cleanup
    cd -
    rm -rf "$TMP_DIR"

    log_success "cc-connect built and installed successfully"
}

install_from_release() {
    log_info "Downloading cc-connect from GitHub releases..."

    local ARCH=$(uname -m)
    local OS=$(uname -s | tr '[:upper:]' '[:lower:]')

    # Map architecture
    case $ARCH in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        armv7l|armhf) ARCH="arm" ;;
        *)
            log_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    local BINARY_NAME="cc-connect-${OS}-${ARCH}"
    local DOWNLOAD_URL

    if [[ "$INSTALL_BETA" == "true" ]]; then
        # Get latest pre-release
        local LATEST_BETA=$(curl -s https://api.github.com/repos/chenhg5/cc-connect/releases | grep "tag_name" | grep -i beta | head -1 | cut -d '"' -f 4)
        if [[ -z "$LATEST_BETA" ]]; then
            log_warn "No beta release found, falling back to latest stable"
            DOWNLOAD_URL="https://github.com/chenhg5/cc-connect/releases/latest/download/${BINARY_NAME}"
        else
            log_info "Found beta release: $LATEST_BETA"
            DOWNLOAD_URL="https://github.com/chenhg5/cc-connect/releases/download/${LATEST_BETA}/${BINARY_NAME}"
        fi
    else
        DOWNLOAD_URL="https://github.com/chenhg5/cc-connect/releases/latest/download/${BINARY_NAME}"
    fi

    log_info "Downloading from: $DOWNLOAD_URL"

    local TMP_FILE="/tmp/cc-connect"
    curl -L -o "$TMP_FILE" "$DOWNLOAD_URL"
    chmod +x "$TMP_FILE"

    mkdir -p "$INSTALL_DIR"
    mv "$TMP_FILE" "$INSTALL_DIR/cc-connect"

    # Ensure PATH includes INSTALL_DIR
    ensure_path

    log_success "cc-connect downloaded and installed successfully"
}

create_config() {
    log_info "Creating configuration..."

    mkdir -p "$CONFIG_DIR"
    mkdir -p "$DATA_DIR"

    local CONFIG_FILE="$CONFIG_DIR/config.toml"

    if [[ -f "$CONFIG_FILE" ]]; then
        log_warn "Configuration file already exists at $CONFIG_FILE"
        log_info "Backing up existing config to $CONFIG_FILE.backup"
        cp "$CONFIG_FILE" "$CONFIG_FILE.backup"
    fi

    # Create minimal config
    cat > "$CONFIG_FILE" << 'EOF'
# cc-connect configuration
# Generated by install-linux.sh

# Language for bot messages: "en", "zh", or "" for auto-detect
language = "zh"

# =============================================================================
# Add your projects below
# =============================================================================
# Each project binds a directory to an agent and messaging platforms.

# [[projects]]
# name = "my-project"
# work_dir = "/home/user/projects/my-project"
# agent = "claudecode"  # or: codex, cursor, gemini, opencode, iflow, qoder
#
# [[projects.platforms]]
# name = "telegram"
# token = "YOUR_BOT_TOKEN"
# allow_from = "YOUR_TELEGRAM_USER_ID"

# [[projects]]
# name = "another-project"
# work_dir = "/home/user/projects/another"
# agent = "codex"
#
# [[projects.platforms]]
# name = "feishu"
# app_id = "YOUR_APP_ID"
# app_secret = "YOUR_APP_SECRET"
# encrypt_key = "YOUR_ENCRYPT_KEY"
# verification_token = "YOUR_VERIFICATION_TOKEN"
# allow_from = "user1,user2"

[log]
level = "info"

# =============================================================================
# Display Settings
# =============================================================================
[display]
thinking_messages = true
thinking_max_len = 300
tool_messages = true
tool_max_len = 500
EOF

    log_success "Configuration file created at $CONFIG_FILE"
    log_warn "Please edit $CONFIG_FILE and add your projects and platforms!"
}

install_daemon() {
    log_info "Installing as systemd daemon..."

    # Check if systemd is available
    if ! command -v systemctl &> /dev/null; then
        log_error "systemctl not found. systemd is required for daemon installation."
        log_info "You can run cc-connect manually with: cc-connect"
        exit 1
    fi

    # Check systemd status
    if ! systemctl is-system-running &> /dev/null; then
        # Check for WSL2
        if grep -qi microsoft /proc/version 2>/dev/null; then
            log_error "systemd is not running in WSL2."
            echo ""
            echo "To enable systemd in WSL2, add the following to /etc/wsl.conf:"
            echo ""
            echo "  [boot]"
            echo "  systemd=true"
            echo ""
            echo "Then restart WSL: wsl --shutdown"
            echo ""
            echo "Alternatively, run cc-connect manually:"
            echo "  nohup cc-connect > ~/.cc-connect/cc-connect.log 2>&1 &"
            exit 1
        fi

        log_warn "systemd is not fully running. Attempting user-level installation..."
    fi

    # Create log directory
    mkdir -p "$(dirname "$LOG_FILE")"

    # Use cc-connect's built-in daemon install
    if "$INSTALL_DIR/cc-connect" daemon install --log "$LOG_FILE" 2>&1; then
        log_success "Daemon installed successfully"
    else
        # Fallback: create systemd unit manually
        create_systemd_unit
    fi

    log_success "cc-connect is running as a daemon service"
    echo ""
    echo "Useful commands:"
    echo "  Status:    systemctl --user status cc-connect"
    echo "  Logs:      tail -f $LOG_FILE"
    echo "  Restart:   systemctl --user restart cc-connect"
    echo "  Stop:      systemctl --user stop cc-connect"
}

create_systemd_unit() {
    log_info "Creating systemd unit manually..."

    local UNIT_DIR="$HOME/.config/systemd/user"
    local UNIT_FILE="$UNIT_DIR/cc-connect.service"

    mkdir -p "$UNIT_DIR"

    cat > "$UNIT_FILE" << EOF
[Unit]
Description=cc-connect - AI Agent Chat Bridge
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/cc-connect
WorkingDirectory=$HOME
Restart=on-failure
RestartSec=10
Environment=CC_LOG_FILE=$LOG_FILE
Environment=CC_LOG_MAX_SIZE=10485760

[Install]
WantedBy=default.target
EOF

    systemctl --user daemon-reload
    systemctl --user enable cc-connect
    systemctl --user start cc-connect
}

show_banner() {
    echo ""
    echo -e "${GREEN}"
    echo "  ╔═══════════════════════════════════════════════════╗"
    echo "  ║                                                   ║"
    echo "  ║   _____ _____ _____    _____ _     _____ _____    ║"
    echo "  ║  /  __ \_   _|  __ \  / ____| |   |_   _/ ____|   ║"
    echo "  ║  | |  | || | | |  | || |    | |     | || |        ║"
    echo "  ║  | |  | || | | |  | || |    | |     | || |        ║"
    echo "  ║  | |__| || |_| |__| || |____| |_____| || |____    ║"
    echo "  ║  |_____/_____|_____/  \_____|______|_____\_____|   ║"
    echo "  ║                                                   ║"
    echo "  ║           AI Agent Chat Bridge                   ║"
    echo "  ╚═══════════════════════════════════════════════════╝"
    echo -e "${NC}"
    echo ""
}

show_next_steps() {
    echo ""
    echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  Installation Complete!${NC}"
    echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "${YELLOW}Next Steps:${NC}"
    echo ""
    echo "1. Edit configuration file:"
    echo "   vim ~/.cc-connect/config.toml"
    echo ""
    echo "2. Add your agent and platform credentials"
    echo "   - Agent: claudecode, codex, cursor, gemini, opencode, etc."
    echo "   - Platform: telegram, feishu, discord, slack, dingtalk, etc."
    echo ""
    echo "3. Restart the service after config changes:"
    echo "   systemctl --user restart cc-connect"
    echo ""
    echo "4. Check logs:"
    echo "   tail -f ~/.cc-connect/cc-connect.log"
    echo ""
    echo -e "${YELLOW}Documentation:${NC}"
    echo "   https://github.com/chenhg5/cc-connect#readme"
    echo ""
}

# Main
main() {
    show_banner

    # Handle uninstall
    if [[ "$UNINSTALL_MODE" == "true" ]]; then
        uninstall
        exit 0
    fi

    check_root
    check_dependencies

    # Install binary
    if [[ "$INSTALL_BETA" == "true" ]]; then
        install_from_release
    elif [[ "$SKIP_BUILD" == "true" ]]; then
        if [[ ! -f "./cc-connect" ]] && [[ ! -f "$INSTALL_DIR/cc-connect" ]]; then
            log_error "No binary found. Run without --skip-build or ensure cc-connect binary exists."
            exit 1
        fi
        if [[ -f "./cc-connect" ]]; then
            mkdir -p "$INSTALL_DIR"
            cp ./cc-connect "$INSTALL_DIR/cc-connect"
            chmod +x "$INSTALL_DIR/cc-connect"
            ensure_path
        fi
    else
        install_from_source
    fi

    # Verify installation
    if ! command -v cc-connect &> /dev/null; then
        log_error "cc-connect binary not found in PATH"
        exit 1
    fi

    # Show version
    log_info "Installed version: $(cc-connect --version 2>/dev/null || echo 'unknown')"

    # Create config
    create_config

    # Install daemon (unless --no-daemon)
    if [[ "$NO_DAEMON" == "false" ]]; then
        install_daemon
    fi

    show_next_steps
}

main "$@"
