#!/bin/sh

# Function to install dependencies
install_dependencies() {
    local missing_deps="$1"
    
    if [ -f "/etc/alpine-release" ]; then
        echo "Installing $missing_deps on Alpine..."
        apk add --no-cache $missing_deps >/dev/null 2>&1 || return 1
    elif [ -f "/etc/debian_version" ]; then
        echo "Installing $missing_deps on Debian/Ubuntu..."
        apt-get update >/dev/null 2>&1 && apt-get install -y $missing_deps >/dev/null 2>&1 || return 1
    elif [ -f "/etc/redhat-release" ]; then
        echo "Installing $missing_deps on RHEL/CentOS..."
        yum install -y $missing_deps >/dev/null 2>&1 || return 1
    else
        echo "ERROR: No package manager found, cannot install $missing_deps" >&2
        return 1
    fi
    return 0
}

# Check for required tools
check_tools() {
    local missing=""
    
    # Check for curl or wget
    if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
        # Prefer curl as it's more common
        missing="curl"
    fi
    
    if [ -n "$missing" ]; then
        echo "Missing dependencies:$missing. Attempting to install..."
        if ! install_dependencies "$missing"; then
            echo "ERROR: Failed to install dependencies" >&2
            exit 1
        fi
        
        # Verify installation
        if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
            echo "ERROR: Dependencies still not available after installation" >&2
            exit 1
        fi
    fi
}

# Extract JSON value without jq
# Usage: extract_json_value '{"result":"0x123"}' "result"
extract_json_value() {
    local json="$1"
    local key="$2"
    
    # Use sed to extract the value for the given key
    # Matches: "key":"value" or "key":value
    echo "$json" | sed -n 's/.*"'"$key"'"\s*:\s*"\?\([^,"}\]*\)"\?.*/\1/p' | head -1
}

# Make HTTP request (supports both curl and wget)
http_post() {
    local url="$1"
    local data="$2"
    
    if command -v curl >/dev/null 2>&1; then
        curl -s -m 2 -X POST "$url" \
            -H "Content-Type: application/json" \
            -d "$data" 2>/dev/null
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O - --timeout=2 --post-data="$data" \
            --header="Content-Type: application/json" \
            "$url" 2>/dev/null
    else
        echo "ERROR: Neither curl nor wget available" >&2
        return 1
    fi
}

# Main health check function
check_chain_head() {
    local el_url="${1:-http://localhost:8545}"
    local block_time="${2:-12}"
    local state_file="${3:-/tmp/chain_head_state}"
    
    # Ensure dependencies are available
    check_tools
    
    # Add wiggle room
    block_time=$(expr "$block_time" + 1)
    
    # Get current block number
    response=$(http_post "$el_url" '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}')
    
    if [ $? -ne 0 ] || [ -z "$response" ]; then
        echo "ERROR: Failed to connect to $el_url"
        exit 1
    fi
    
    # Extract the hex block number from JSON response
    hex_block=$(extract_json_value "$response" "result")
    
    if [ -z "$hex_block" ] || [ "$hex_block" = "null" ]; then
        echo "ERROR: Failed to get block number from response"
        exit 1
    fi
    
    # Convert hex to decimal
    current_block=$(printf "%d" "$hex_block")
    current_time=$(date +%s)
    
    # Read previous state
    if [ -f "$state_file" ]; then
        read -r prev_block prev_time < "$state_file"
    else
        # First run - just save state and succeed
        echo "$current_block $current_time" > "$state_file"
        echo "OK: Initial check, block $current_block"
        exit 0
    fi
    
    # Check if block advanced
    if [ "$current_block" -gt "$prev_block" ]; then
        # Block advanced - update state and succeed
        echo "$current_block $current_time" > "$state_file"
        echo "OK: Chain head advanced to $current_block"
        exit 0
    fi
    
    # Block hasn't advanced - check if we've exceeded timeout
    elapsed=$(expr "$current_time" - "$prev_time")
    
    if [ "$elapsed" -ge "$block_time" ]; then
        echo "ERROR: Chain head stuck at $current_block for ${elapsed}s (timeout: ${block_time}s)"
        exit 1
    fi
    
    # Block hasn't advanced but still within timeout
    echo "OK: Chain head at $current_block, waiting ${elapsed}/${block_time}s"
    exit 0
}

# Run the check
check_chain_head "$@"
