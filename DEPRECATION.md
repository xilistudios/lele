# Deprecation Notice: fmod, preview, and apply tools

**Date**: 2026-03-15  
**Status**: ⚠️ DEPRECATED

## Summary

The following tools and skills have been deprecated and removed from lele:

### Deprecated Tools
- `preview` - File preview tool (replaced by git worktree)
- `apply` - File apply tool (replaced by git worktree)
- `fmod` - File modification tool (replaced by git worktree)

### Deprecated Skills
- `fmod` - File modification skill

## Migration Guide

### Old Workflow (Deprecated)
```bash
# Old workflow using preview/apply
lele agent -m "edit file.txt to add content"
# This would create .fmod.tmp files and require preview/apply
```

### New Workflow (Recommended)
```bash
# Use git worktree for safe editing
git worktree add -b feature-branch ../worktree-feature
# Edit files directly in the worktree
# Commit changes
cd ../worktree-feature
git add .
git commit -m "Changes"
git push
```

## Breaking Changes

### Removed Tools
- ❌ `preview` - No longer available
- ❌ `apply` - No longer available  
- ❌ `fmod` - No longer available

### Removed Skills
- ❌ `fmod` skill - No longer available

### What Changed
- `.fmod.tmp` temporary files are no longer created
- No automatic backup system for file edits
- Users should use git worktree for safe parallel editing

## Why This Change?

The preview/apply/fmod tools were designed for a temporary file-based editing workflow. However, this approach has several limitations:

1. **Complexity**: Multiple temp files to manage
2. **Risk**: Potential for lost changes if not applied properly
3. **No versioning**: No built-in history or rollback

The recommended `git worktree` approach provides:
1. ✅ **Isolation**: Clean separation of work in progress
2. ✅ **Version control**: Full git history and branching
3. ✅ **Safety**: Easy rollback and comparison
4. ✅ **Parallel work**: Multiple worktrees for different features

## Migration Steps

If you were using the deprecated tools:

1. **Stop using** `preview`, `apply`, and `fmod` tools
2. **Install git** if not already available
3. **Use git worktree** for safe editing workflows
4. **Update your scripts** to remove references to deprecated tools

## Related Files Removed

- `pkg/tools/preview.go`
- `pkg/tools/apply.go`
- `pkg/tools/fmod.go`
- `pkg/tools/fmod_test.go`
- `cmd/lele/workspace/skills/fmod/`
- `workspace/skills/fmod/`

## Questions?

Refer to the git worktree documentation: `git help worktree`

---

*This deprecation is part of lele's ongoing effort to simplify and improve the editing workflow.*
