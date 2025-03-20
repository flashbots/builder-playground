#!/bin/bash

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 source_folder destination_folder"
    exit 1
fi

source_dir="$1"
dest_dir="$2"

# Check if source directory exists
if [ ! -d "$source_dir" ]; then
    echo "Error: Source directory '$source_dir' does not exist"
    exit 1
fi

# Create destination directory if it doesn't exist
mkdir -p "$dest_dir"

# First, copy everything
cp -r "$source_dir"/* "$dest_dir"/ 2>/dev/null || true

# Remove any data_* directories from the destination
find "$dest_dir" -type d -name "data_*" -exec rm -rf {} +

echo "Copied contents from '$source_dir' to '$dest_dir' (excluding data_* folders)"
