#!/bin/bash
# Build script for Pyarmor obfuscation with ARM (AArch64) support
# Usage: ./build_arm.sh <entry_script.py> <output_dir>

ENTRY_SCRIPT=${1:-app.py}
OUTPUT_DIR=${2:-dist}

ARCH=$(uname -m)

if [[ "$ARCH" == "aarch64" ]]; then
    PLATFORM="linux.aarch64"
    echo "Detected ARM64 architecture. Using platform: $PLATFORM"
else
    PLATFORM=""
    echo "Detected architecture: $ARCH. Using default platform."
fi

if [[ -n "$PLATFORM" ]]; then
    pyarmor gen --platform $PLATFORM --output "$OUTPUT_DIR" "$ENTRY_SCRIPT"
else
    pyarmor gen --output "$OUTPUT_DIR" "$ENTRY_SCRIPT"
fi
