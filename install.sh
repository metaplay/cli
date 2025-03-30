#!/usr/bin/env bash
#
# installer for the Metaplay CLI 'metaplay'
# works on Linux/macOS and x64/arm64.
# usage: ./install.sh [--version X.Y.Z] [--verbose]

# How to use:
#
# Install the latest version (auto-detect platform):
# curl -sSfL https://raw.githubusercontent.com/metaplay/cli/main/install.sh | bash
# Install a specific version:
# curl -sSfL https://raw.githubusercontent.com/metaplay/cli/main/install.sh | bash -s -- --version 1.2.3
# Enable verbose mode:
# curl -sSfL https://raw.githubusercontent.com/metaplay/cli/main/install.sh | bash -s -- --verbose

{ # this ensures the entire script is downloaded #

set -e

# --- Adjustable variables -------------------------------------
REPO="metaplay/cli"             # GitHub repository
BINARY_NAME="metaplay"          # The name of the binary to install
INSTALL_DIR="$HOME/.local/bin"  # Install to user's home to avoid needing sudo
DOWNLOAD_BASE="https://github.com/${REPO}/releases/download"
# ---------------------------------------------------------------

VERSION=""      # if empty, will fetch latest
VERBOSE="false"

# Colored output helpers
COLOR_GREEN="$(printf '\033[32m')"
COLOR_BLUE="$(printf '\033[34m')"
COLOR_BOLD="$(printf '\033[1m')"
COLOR_RESET="$(printf '\033[0m')"

print_info() {
  printf "[installer] %s\n" "$1"
}
print_success() {
  printf "%s[installer] %s%s\n" "$COLOR_GREEN" "$1" "$COLOR_RESET"
}
print_verbose() {
  if [ "$VERBOSE" = "true" ]; then
    printf "[verbose] %s\n" "$1"
  fi
}
print_warning() {
  printf "%s[installer] Warning: %s%s\n" "$COLOR_BOLD" "$1" "$COLOR_RESET" >&2
}
print_error() {
  printf "%s[installer] ERROR: %s%s\n" "$COLOR_BOLD" "$1" "$COLOR_RESET" >&2
}

usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Options:
  --version X.Y.Z   Install a specific version (e.g. 1.2.3).
  --verbose         Enable verbose (debug) output.
  -h, --help        Show this help message.

By default, installs the latest version for your platform.
EOF
}

# Parse arguments
while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      VERSION="$2"
      shift 2
      ;;
    --version=*)
      VERSION="${1#*=}"
      shift 1
      ;;
    --verbose)
      VERBOSE="true"
      shift 1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      print_error "Unknown argument: $1"
      usage
      exit 1
      ;;
  esac
done

# Detect OS
UNAME_OS="$(uname -s)"
case "$UNAME_OS" in
  Linux*)   OS="Linux" ;;
  Darwin*)  OS="Darwin" ;;
  *)
    print_error "Unsupported OS: $UNAME_OS"
    exit 1
    ;;
esac

# Detect architecture
UNAME_ARCH="$(uname -m)"
case "$UNAME_ARCH" in
  x86_64|amd64)   ARCH="x86_64" ;;
  arm64|aarch64)  ARCH="arm64" ;;
  *)
    print_error "Unsupported architecture: $UNAME_ARCH"
    exit 1
    ;;
esac

print_verbose "OS: $OS"
print_verbose "Arch: $ARCH"
print_verbose "Install dir: $INSTALL_DIR"

# If no VERSION specified, discover latest via GitHub API
if [ -z "$VERSION" ]; then
  print_verbose "No version specified. Finding latest official release..."
  VERSION=$(curl -sSfL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
  print_verbose "Latest official version is: $VERSION"
elif [ "$VERSION" = "latest-dev" ]; then
  print_verbose "Version specified as 'latest-dev'. Finding latest development release..."
  # Fetch all releases (newest first), get the tag_name of the very first one
  VERSION=$(curl -sSfL "https://api.github.com/repos/${REPO}/releases" | grep '"tag_name":' | head -n 1 | sed -E 's/.*"([^"]+)".*/\1/')
  print_verbose "Latest development version is: $VERSION"
fi

# Validate that a version was determined
if [ -z "$VERSION" ]; then
  print_error "Could not determine a version to install."
  exit 1
fi

# Construct the download URL
TARBALL="MetaplayCLI_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="${DOWNLOAD_BASE}/${VERSION}/${TARBALL}"

print_info "Installing '${BINARY_NAME}' v${VERSION} for ${OS}/${ARCH} to ${INSTALL_DIR}..."

print_verbose "Download URL: ${DOWNLOAD_URL}"

# Download the tarball (show progress bar unless verbose)
TMP_DIR="$(mktemp -d)"
TARBALL_PATH="${TMP_DIR}/${TARBALL}"

if [ "$VERBOSE" = "true" ]; then
  curl -sSfL -o "${TARBALL_PATH}" "${DOWNLOAD_URL}"
else
  # Show compact progress bar
  curl --progress-bar -fL -o "${TARBALL_PATH}" "${DOWNLOAD_URL}"
fi

# Extract the binary
tar -C "${TMP_DIR}" -xzf "${TARBALL_PATH}"

# Move to /usr/local/bin (may need sudo)
EXTRACTED_BINARY="${TMP_DIR}/${BINARY_NAME}"
if [ ! -f "${EXTRACTED_BINARY}" ]; then
  print_error "Downloaded archive does not contain the expected binary '${BINARY_NAME}'."
  exit 1
fi

chmod +x "${EXTRACTED_BINARY}"
# print_info "Installing to ${INSTALL_DIR}."

# Ensure the directory exists
mkdir -p "$INSTALL_DIR"

if [ ! -w "${INSTALL_DIR}" ]; then
  sudo mv "${EXTRACTED_BINARY}" "${INSTALL_DIR}/${BINARY_NAME}"
else
  mv "${EXTRACTED_BINARY}" "${INSTALL_DIR}/${BINARY_NAME}"
fi

# Ensure binary is executable
chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

# Clean up
rm -rf "${TMP_DIR}"

# Ensure INSTALL_DIR is in PATH
if ! echo "$PATH" | tr ':' '\n' | grep -q "$INSTALL_DIR"; then
  print_info "Note: $HOME/.local/bin is not your PATH."

  SHELL_PROFILE=""
  if [ -n "$ZSH_VERSION" ]; then
    SHELL_PROFILE="$HOME/.zshrc"
  elif [ -n "$BASH_VERSION" ]; then
    SHELL_PROFILE="$HOME/.bashrc"
  elif [ -n "$FISH_VERSION" ]; then
    SHELL_PROFILE="$HOME/.config/fish/config.fish"
  elif [ -f "$HOME/.profile" ]; then
    SHELL_PROFILE="$HOME/.profile"
  fi

  if [ -n "$SHELL_PROFILE" ]; then
    print_info "Updating your $SHELL_PROFILE to include $INSTALL_DIR in PATH..."
    echo "" >> $SHELL_PROFILE
    echo "export PATH=\"\$HOME/.local/bin:\$PATH\"" >> $SHELL_PROFILE
    print_info "Load the updated path in your shell session with:"
    echo "  source $SHELL_PROFILE"
  else
    print_info "Manually add this to your shell profile:"
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
  fi

  # Add to PATH temporarily (for the verification step)
  export PATH="$HOME/.local/bin:$PATH"
fi

# Verify installation
if command -v "${BINARY_NAME}" >/dev/null 2>&1; then
  INSTALLED_VERSION="$(${BINARY_NAME} version 2>/dev/null || true)"
  print_success "'${BINARY_NAME}' v${INSTALLED_VERSION} successfully installed!"
else
  print_error "Something went wrong: binary not found on PATH."
  exit 1
fi

exit 0

} # this ensures the entire script is downloaded #
