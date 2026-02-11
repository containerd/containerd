#!/bin/bash

#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

# Script to generate EROFS test images for fsview tests
# Usage: sudo ./generate-erofs.sh
#
# Note: Requires root to create whiteout char devices and set trusted.* xattrs

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Get the actual user (in case running with sudo)
if [[ -n "$SUDO_USER" ]]; then
    ACTUAL_USER="$SUDO_USER"
else
    ACTUAL_USER="$USER"
fi

echo "Generating EROFS test images..."

# Check if mkfs.erofs is available
if ! command -v mkfs.erofs &> /dev/null; then
    echo "Error: mkfs.erofs not found. Please install erofs-utils."
    exit 1
fi

# Check if running as root
if [[ $EUID -ne 0 ]]; then
   echo "Error: This script must be run as root (use sudo)"
   exit 1
fi

# Clean up old images
rm -f base.erofs upper1.erofs upper2.erofs

# ============================================================================
# Base layer - Contains basic directory structure with files
# ============================================================================
echo "Creating base layer..."
rm -rf base
mkdir -p base/dir1 base/dir2/subdir

cat > base/dir1/file1.txt <<EOF
file1 content
EOF

cat > base/dir1/file2.txt <<EOF
file2 content
EOF

cat > base/dir2/file2.txt <<EOF
file2 content
EOF

cat > base/dir2/subdir/subfile.txt <<EOF
subfile content
EOF

# Generate base.erofs (uncompressed)
mkfs.erofs base.erofs base
chown "$ACTUAL_USER:$ACTUAL_USER" base.erofs
echo "✓ Generated base.erofs"

# ============================================================================
# Upper1 layer - Adds new file and whiteouts to demonstrate overlay
# ============================================================================
echo "Creating upper1 layer..."
rm -rf upper1
mkdir -p upper1/dir1 upper1/dir2

# Add a new file
cat > upper1/dir1/newfile.txt <<EOF
new file content
EOF

# Create whiteout for file1.txt and file2.txt (hides them from base layer)
# Whiteouts are character devices with major:minor 0:0
mknod upper1/dir1/file1.txt c 0 0
mknod upper1/dir1/file2.txt c 0 0
echo "  Created whiteout char devices for file1.txt and file2.txt"

# Mark dir1 as opaque using xattr (hides all files from lower layers in dir1)
setfattr -n trusted.overlay.opaque -v y upper1/dir1
echo "  Set opaque xattr on dir1"

# Generate upper1.erofs (uncompressed, needs sudo to read trusted.* xattrs)
mkfs.erofs upper1.erofs upper1
chown "$ACTUAL_USER:$ACTUAL_USER" upper1.erofs
echo "✓ Generated upper1.erofs"

# ============================================================================
# Upper2 layer - Tests whiteout detection
# ============================================================================
echo "Creating upper2 layer..."
rm -rf upper2
mkdir -p upper2/dir1

# Create a whiteout char device for file1.txt (for whiteout detection test)
mknod upper2/dir1/file1.txt c 0 0
echo "  Created whiteout char device for file1.txt"

# Generate upper2.erofs (uncompressed, needs sudo to read trusted.* xattrs)
mkfs.erofs upper2.erofs upper2
chown "$ACTUAL_USER:$ACTUAL_USER" upper2.erofs
echo "✓ Generated upper2.erofs"

# ============================================================================
# Summary
# ============================================================================
echo ""
echo "Summary of generated images:"
echo "  base.erofs   - Base layer with original files"
echo "  upper1.erofs - Layer with new file + whiteouts (file1.txt, file2.txt as char 0:0)"
echo "                 + opaque dir1 (trusted.overlay.opaque=y)"
echo "  upper2.erofs - Layer with whiteout file1.txt (char 0:0) for whiteout detection test"
echo ""
echo "All images owned by: $ACTUAL_USER"
echo ""
echo "To add new test cases:"
echo "  1. Modify the sections above to add/change files"
echo "  2. Run: sudo ./generate-erofs.sh"
echo ""
echo "Done!"
