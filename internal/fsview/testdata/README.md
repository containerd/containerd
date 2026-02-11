# EROFS Test Data

This directory contains EROFS filesystem images used for testing the fsview overlay implementation.

## Generating Test Images

To regenerate the EROFS test images:

```bash
sudo ./generate-erofs.sh
```

**Note:** Requires root privileges to create whiteout character devices and set `trusted.overlay.opaque` xattrs.

## Test Image Structure

### base.erofs
Base layer containing the original filesystem structure:
```
base/
├── dir1/
│   ├── file1.txt (content: "file1 content")
│   └── file2.txt (content: "file2 content")
└── dir2/
    ├── file2.txt (content: "file2 content")
    └── subdir/
        └── subfile.txt (content: "subfile content")
```

### upper1.erofs
Upper layer demonstrating whiteouts and opaque directories:
```
upper1/
├── dir1/ (opaque via trusted.overlay.opaque=y xattr)
│   ├── newfile.txt (content: "new file content")
│   ├── file1.txt (char device 0:0 - whiteout)
│   └── file2.txt (char device 0:0 - whiteout)
└── dir2/
```

**Effect when overlayed on base:**
- `dir1/newfile.txt` - new file added
- `dir1/file1.txt` - hidden by whiteout (redundant with opaque dir)
- `dir1/file2.txt` - hidden by whiteout (redundant with opaque dir)
- `dir1/*` from base - all contents hidden by opaque directory
- `dir2/file2.txt` - visible from base layer (dir2 is not opaque)

### upper2.erofs
Layer for testing whiteout detection:
```
upper2/
└── dir1/
    └── file1.txt (char device 0:0 - whiteout)
```

**Purpose:**
- Tests that whiteout char devices are correctly detected by `isWhiteout()`
- `file1.txt` is a char device 0:0 that should be recognized as a whiteout marker

## Overlay Semantics

### Whiteouts
- Represented as character devices with major:minor 0:0
- Hide files/directories with the same name in lower layers
- Created with: `mknod <path> c 0 0`

### Opaque Directories
- Marked with xattr: `trusted.overlay.opaque=y`
- Hide all contents from lower layers in that directory
- Set with: `setfattr -n trusted.overlay.opaque -v y <dir>`

## Adding New Test Cases

1. Edit `generate-erofs.sh` to add new directories/files
2. Run `sudo ./generate-erofs.sh` to regenerate images
3. Update this README to document the changes
4. Add corresponding tests in `overlay_erofs_test.go` or `mount_format_test.go`

## Example: Adding a New Layer

```bash
# In generate-erofs.sh, add a new section:
echo "Creating upper3 layer..."
rm -rf upper3
mkdir -p upper3/dir3

cat > upper3/dir3/newfile.txt <<EOF
upper3 content
EOF

mkfs.erofs upper3.erofs upper3
chown "$ACTUAL_USER:$ACTUAL_USER" upper3.erofs
```
