# FMOD Module Fixes - Code Review Implementation

**Date:** 2026-02-26  
**Status:** ✅ Completed  
**Tests:** All fmod-related tests passing

## Summary of Changes

Based on the code review, implemented critical fixes for the fmod module in picoclaw:

### 1. ✅ Fixed UTF-8 BOM Bug (HIGH PRIORITY)

**File:** `pkg/tools/fmod.go`

**Problem:** 
- `DetectEncoding` was returning "UTF-8" for both files with and without BOM
- `GetBOM("UTF-8")` was incorrectly expected to return BOM bytes
- This caused all UTF-8 files to get a BOM added when saved, even if they didn't have one originally

**Solution:**
- `DetectEncoding` now correctly returns "UTF-8-BOM" when BOM is detected (EF BB BF)
- `DetectEncoding` returns "UTF-8" (no BOM) when no BOM is present
- `GetBOM("UTF-8")` correctly returns `nil` (no BOM)
- `GetBOM("UTF-8-BOM")` returns the BOM bytes `[0xEF, 0xBB, 0xBF]`
- `WriteFileWithEncoding` only adds BOM if the encoding explicitly includes it

**Code Changes:**
```go
// DetectEncoding now distinguishes UTF-8 vs UTF-8-BOM
if buffer[0] == 0xEF && buffer[1] == 0xBB && buffer[2] == 0xBF {
    return "UTF-8-BOM", buffer[3:]  // Was: "UTF-8-BOM" (correct)
}
return "UTF-8", buffer  // Correctly indicates no BOM

// GetBOM only returns BOM for explicit UTF-8-BOM encoding
func GetBOM(encoding string) []byte {
    switch strings.ToUpper(encoding) {
    case "UTF-8-BOM":
        return []byte{0xEF, 0xBB, 0xBF}
    // ... other encodings
    }
    return nil  // UTF-8 without BOM returns nil
}
```

### 2. ✅ Fixed apply.go for True Atomicity (MEDIUM PRIORITY)

**File:** `pkg/tools/apply.go`

**Problem:**
- The function claimed to be atomic but used backup + direct write
- In case of interruption/failure, the file could be left in inconsistent state

**Solution:**
- Implemented true atomic write pattern:
  1. Write content to temporary file (`.fmod.tmp.final`)
  2. Use `os.Rename()` to atomically replace original
- This ensures the original file is never in an inconsistent state
- On POSIX systems, `rename()` is guaranteed to be atomic

**Code Changes:**
```go
// OLD: Backup + direct write (not atomic)
backupPath := resolvedPath + ".fmod.bak"
os.WriteFile(backupPath, originalContent, 0644)
os.WriteFile(resolvedPath, tempContent, 0644)

// NEW: True atomic write via rename
tempFinalPath := resolvedPath + ".fmod.tmp.final"
os.WriteFile(tempFinalPath, tempContent, originalMode)
os.Rename(tempFinalPath, resolvedPath)  // Atomic!
```

### 3. ✅ Fixed Permission Preservation (MEDIUM PRIORITY)

**File:** `pkg/tools/apply.go`

**Problem:**
- Used hardcoded `0644` permissions for both backup and final write
- Could change executable bits or other permission flags

**Solution:**
- Read original file permissions with `os.Stat()` before any modifications
- Preserve and apply original permissions to the new file
- Uses `originalInfo.Mode()` to get exact permission bits

**Code Changes:**
```go
// Get original permissions first
originalInfo, err := os.Stat(resolvedPath)
originalMode := originalInfo.Mode()

// Apply original permissions to new file
os.WriteFile(tempFinalPath, tempContent, originalMode)
```

### 4. ℹ️ Legacy Tools Coexistence (LOW PRIORITY / ARCHITECTURAL)

**File:** `pkg/agent/instance.go`

**Status:** Not changed - requires architectural decision

**Observation:**
- Both legacy tools (`write_file`, `edit_file`) and fmod tools are registered
- This allows gradual migration but could cause confusion

**Recommendation:**
- Option A: Remove legacy tools when fmod is mature
- Option B: Add configuration flag to force fmod-only mode
- Option C: Keep both for backward compatibility (current state)

## Test Results

All fmod-related tests pass:

```bash
$ go test ./pkg/tools/...
ok      github.com/sipeed/picoclaw/pkg/tools    10.186s

$ go test ./pkg/agent -run TestSubagentManager_FmodTools
PASS
ok      github.com/sipeed/picoclaw/pkg/agent    0.015s
```

### Updated Tests

Modified test expectations in `pkg/tools/fmod_test.go`:

1. **TestDetectEncoding/UTF-8_with_BOM**: Now expects "UTF-8-BOM" instead of "UTF-8"
2. **TestGetBOM**: Updated to expect `nil` for "UTF-8" and proper BOM for "UTF-8-BOM"

## Files Modified

1. `pkg/tools/fmod.go` - Encoding detection and BOM handling
2. `pkg/tools/apply.go` - Atomic write and permission preservation
3. `pkg/tools/fmod_test.go` - Updated test expectations

## Verification Steps

To verify the fixes work correctly:

```bash
# Test UTF-8 without BOM preservation
echo "hello world" > /tmp/test.txt
# Edit with smart_edit, then apply
# Result: File should remain UTF-8 without BOM

# Test UTF-8 with BOM preservation
printf '\xEF\xBB\xBFhello world' > /tmp/test-bom.txt
# Edit with smart_edit, then apply  
# Result: File should keep UTF-8 BOM

# Test permission preservation
chmod 755 /tmp/script.sh
# Edit with smart_edit, then apply
# Result: File should remain executable (755)
```

## Impact Assessment

- **Backward Compatibility:** ✅ Maintained - files without BOM stay without BOM
- **Data Integrity:** ✅ Improved - atomic writes prevent corruption
- **Security:** ✅ Improved - preserves file permissions correctly
- **Performance:** ↔️ Neutral - same number of I/O operations

---

*Implementation completed by Michu 🦞 on 2026-02-26*
