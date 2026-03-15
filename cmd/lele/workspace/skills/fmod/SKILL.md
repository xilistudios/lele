---
name: fmod
description: Intelligent file modification tools with fallback strategies and safe editing workflow
---

# Fmod - File Modification Tools

Fmod provides a set of intelligent file editing tools designed to handle complex editing tasks with fallback strategies and a safe editing workflow.

## Overview

Fmod introduces a **temp file workflow** for safe editing:
1. **Edit** → Create a temp file (.fmod.tmp) with changes
2. **Preview** → Review changes before committing
3. **Apply** → Commit changes to the original file

This workflow prevents accidental data corruption and allows you to review changes before making them permanent.

## Tools

### smart_edit

Edit a file with intelligent fallback strategies. Creates a temp file (.fmod.tmp) with the changes.

**Features:**
- **Exact matching**: Tries literal string match first
- **Whitespace-tolerant fallback**: If exact match fails, ignores whitespace differences
- **Regex support**: Optional regex mode with flags (g=global, i=case-insensitive)
- **Multiple match detection**: Reports error if old_text appears multiple times

**Parameters:**
- `path` (required): Path to the file to edit
- `old_text` (required): Text to find and replace
- `new_text` (required): Replacement text
- `regex` (optional): Use regex matching (default: false)
- `flags` (optional): Regex flags like "gi"

**Example:**
```json
{
  "path": "main.go",
  "old_text": "func main() {",
  "new_text": "func main() {\n\t// Entry point"
}
```

**Workflow:**
1. Call smart_edit → Creates .fmod.tmp file
2. Call preview → Review changes
3. Call apply → Commit changes

---

### preview

Preview the temp file (.fmod.tmp) with optional line range filtering.

**Parameters:**
- `path` (required): Path to the original file (reads .fmod.tmp)
- `from` (optional): Start line (1-indexed)
- `to` (optional): End line (1-indexed)

**Example:**
```json
{
  "path": "main.go",
  "from": 1,
  "to": 20
}
```

---

### apply

Apply changes from the temp file (.fmod.tmp) to the original file.

This overwrites the original file with the content from the temp file and removes both temp and backup files.

**Parameters:**
- `path` (required): Path to the original file

**Example:**
```json
{
  "path": "main.go"
}
```

---

### patch

Apply a unified diff to a file. Creates a temp file with the patched content.

Supports standard unified diff format with multiple hunks.

**Parameters:**
- `path` (required): Path to the file to patch
- `diff` (required): Unified diff content (or @path to read from file)

**Example:**
```json
{
  "path": "main.go",
  "diff": "@@ -1,5 +1,5 @@\n package main\n \n-func main() {\n+func main() { // entry\n     run()\n }"
}
```

Or read from file:
```json
{
  "path": "main.go",
  "diff": "@/path/to/changes.diff"
}
```

---

### sequential_replace

Perform multiple replacements in a single operation. Detects overlaps and conflicts.

**Features:**
- Multiple replacements in one operation
- Automatic overlap/conflict detection
- Applies replacements from end to start to maintain correct indices

**Parameters:**
- `path` (required): Path to the file
- `pairs` (required): JSON array of {old, new} objects
- `regex` (optional): Use regex matching (default: false)
- `flags` (optional): Regex flags

**Example:**
```json
{
  "path": "main.go",
  "pairs": "[{\"old\": \"func\", \"new\": \"function\"}, {\"old\": \"main\", \"new\": \"entry\"}]"
}
```

---

### read_file (enhanced)

The existing read_file tool now supports line range selection.

**Parameters:**
- `path` (required): Path to the file
- `from` (optional): Start line (1-indexed)
- `to` (optional): End line (1-indexed)

---

## Best Practices

### Single Changes
For simple edits:
1. Use `smart_edit` to make changes
2. Use `preview` to verify
3. Use `apply` to commit

### Multiple Related Changes
For multiple changes to the same file:
1. Use `sequential_replace` with all changes
2. Use `preview` to verify
3. Use `apply` to commit

### Complex Changes (Diffs)
When you have a complex set of changes:
1. Use `patch` with a unified diff
2. Use `preview` to verify
3. Use `apply` to commit

### Error Handling
- If `smart_edit` reports "old_text appears X times", make your old_text more specific
- If `sequential_replace` reports overlaps, split into multiple calls
- Always preview before applying to catch unexpected changes

---

## Common Patterns

### Adding a new function
```json
// 1. List the file to see current content
{"path": "main.go"}

// 2. Edit to add the function
{
  "path": "main.go",
  "old_text": "func main() {",
  "new_text": "func helper() {\n\t// Helper function\n}\n\nfunc main() {"
}

// 3. Preview changes
{"path": "main.go"}

// 4. Apply if satisfied
{"path": "main.go"}
```

### Replacing multiple imports
```json
{
  "path": "main.go",
  "pairs": "[
    {\"old\": \"import \\\"fmt\\\"\", \"new\": \"import (\\n\\t\\\"fmt\\\"\\n\\t\\\"log\\\"\\n)\"}
  ]"
}
```

---

## Technical Details

### Encoding Support
All fmod tools preserve file encoding including:
- UTF-8 (with or without BOM)
- UTF-16 BE/LE
- UTF-32 BE/LE

### Temp File Lifecycle
- `.fmod.tmp` - Contains pending changes
- `.fmod.bak` - Backup of original (created during apply)
- Both are cleaned up after successful apply

### Safety Features
- All edits go to temp files first
- Original file is backed up before apply
- Atomic rename operations where possible
- Validation of hunks before patching
