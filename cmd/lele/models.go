package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/xilistudios/lele/pkg/catalog"
)

func modelsCmd() {
	if len(os.Args) < 3 {
		modelsHelp()
		return
	}
	switch os.Args[2] {
	case "refresh":
		modelsRefreshCmd()
	case "list":
		modelsListCmd()
	case "providers":
		modelsProvidersCmd()
	default:
		fmt.Printf("Unknown models command: %s\n", os.Args[2])
		modelsHelp()
		os.Exit(1)
	}
}

func modelsHelp() {
	fmt.Println(`lele models — model catalog

Usage:
  lele models refresh              Download/update the catalog cache from GitHub
  lele models providers            List catalog providers
  lele models list [provider]      List models (optionally for one provider)

Cache: ~/.lele/cache/catalog/
Remote: https://raw.githubusercontent.com/xilistudios/lele/main/catalog
Env:    LELE_CATALOG_BASE_URL to override the remote base.`)
}

func modelsRefreshCmd() {
	fmt.Println("Refreshing model catalog from GitHub…")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	err := catalog.Refresh(ctx, catalog.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Catalog refresh failed: %v\n", err)
		// Still show whatever is cached.
		if !catalog.HasCachedIndex() {
			os.Exit(1)
		}
		fmt.Println("Using previously cached catalog.")
	} else {
		fmt.Printf("Catalog updated in %s\n", time.Since(start).Round(time.Millisecond))
	}

	ids := catalog.KnownProviderTypes()
	models := 0
	for _, id := range ids {
		models += len(catalog.ModelsForProvider(id))
	}
	fmt.Printf("Providers: %d  Models: %d\n", len(ids), models)
	fmt.Printf("Cache: %s\n", catalog.CacheDir())
}

func modelsProvidersCmd() {
	catalog.Ensure()
	ids := catalog.KnownProviderTypes()
	if len(ids) == 0 {
		fmt.Println("No catalog providers. Run: lele models refresh")
		return
	}
	sort.Strings(ids)
	for _, id := range ids {
		p, ok := catalog.ProviderByID(id)
		if !ok {
			continue
		}
		n := len(catalog.ModelsForProvider(id))
		base := p.APIBase
		if base == "" {
			base = catalog.DefaultAPIBaseByType(id)
		}
		fmt.Printf("%-22s %-28s %4d models  %s\n", id, p.Name, n, base)
	}
}

func modelsListCmd() {
	catalog.Ensure()
	if len(os.Args) >= 4 {
		provider := os.Args[3]
		models := catalog.ModelsForProvider(provider)
		if len(models) == 0 {
			fmt.Printf("No models for %q. Try: lele models refresh\n", provider)
			return
		}
		for _, m := range models {
			printModelLine(m)
		}
		return
	}

	ids := catalog.KnownProviderTypes()
	sort.Strings(ids)
	any := false
	for _, id := range ids {
		models := catalog.ModelsForProvider(id)
		if len(models) == 0 {
			continue
		}
		any = true
		fmt.Printf("\n%s (%d)\n", id, len(models))
		for _, m := range models {
			printModelLine(m)
		}
	}
	if !any {
		fmt.Println("No catalog models cached. Run: lele models refresh")
	}
}

func printModelLine(m catalog.Model) {
	flags := ""
	if m.Vision {
		flags += " vision"
	}
	if len(m.ThinkingLevels) > 0 {
		flags += " thinking"
	}
	cw := ""
	if m.ContextWindow > 0 {
		cw = fmt.Sprintf("  ctx=%d", m.ContextWindow)
	}
	fmt.Printf("  %-40s%s%s\n", m.ID, cw, flags)
}
