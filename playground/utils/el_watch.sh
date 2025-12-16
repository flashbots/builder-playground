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
    
    if ! command -v curl >/dev/null 2>&1; then
        missing="curl"
    fi
    
    if ! command -v jq >/dev/null 2>&1; then
        missing="$missing jq"
    fi
    
    if [ -n "$missing" ]; then
        echo "Missing dependencies:$missing. Attempting to install..."
        if ! install_dependencies "$missing"; then
            echo "ERROR: Failed to install dependencies" >&2
            exit 1
        fi
        
        # Verify installation
        if ! command -v curl >/dev/null 2>&1 || ! command -v jq >/dev/null 2>&1; then
            echo "ERROR: Dependencies still not available after installation" >&2
            exit 1
        fi
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
    block_time=$((block_time + 1))
    
    # Get current block number
    response=$(curl -s -m 2 -X POST "$el_url" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' 2>/dev/null)
    
    if [ $? -ne 0 ]; then
        echo "ERROR: curl failed to connect to $el_url"
        exit 1
    fi
    
    hex_block=$(echo "$response" | jq -r '.result' 2>/dev/null)
    
    if [ -z "$hex_block" ] || [ "$hex_block" = "null" ]; then
        echo "ERROR: Failed to get block number from response"
        exit 1
    fi
    
    current_block=$((hex_block))
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
    elapsed=$((current_time - prev_time))
    
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
