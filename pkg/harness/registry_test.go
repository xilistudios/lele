// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package harness

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func cmd(name, desc string) *Command {
	return &Command{Name: name, Description: desc, Source: SourceConfig}
}

func names(cs []*Command) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Name)
		b.WriteString(" ")
	}
	return strings.TrimSpace(b.String())
}

func TestRegistryRegisterOverwrite(t *testing.T) {
	r := NewRegistry()
	r.Register(cmd("foo", "first"))
	r.Register(cmd("FOO", "second")) // same name, case-insensitive overwrite
	got, ok := r.Get("foo")
	if !ok || got.Description != "second" {
		t.Fatalf("Get(foo) = %+v %v, want description \"second\"", got, ok)
	}
	if r.Len() != 1 {
		t.Fatalf("Len = %d, want 1", r.Len())
	}
}

func TestRegistryGetCaseInsensitive(t *testing.T) {
	r := NewRegistry()
	r.Register(cmd("MiX", "x"))
	if _, ok := r.Get("mix"); !ok {
		t.Error(`Get("mix") failed`)
	}
	if _, ok := r.Get("MiX"); !ok {
		t.Error(`Get("MiX") failed`)
	}
	if _, ok := r.Get("MIX"); !ok {
		t.Error(`Get("MIX") failed`)
	}
	stored, _ := r.Get("mix")
	if stored.Name != "mix" {
		t.Errorf("stored Name = %q, want lowercased \"mix\"", stored.Name)
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("Get(nope) should miss")
	}
}

func TestRegistryRegisterDoesNotMutateCaller(t *testing.T) {
	r := NewRegistry()
	c := cmd("Up", "d")
	r.Register(c)
	if c.Name != "Up" {
		t.Errorf("caller command mutated: Name = %q", c.Name)
	}
}

func TestRegistryAllSortedCopy(t *testing.T) {
	r := NewRegistry()
	r.Replace([]*Command{cmd("zulu", ""), cmd("alpha", ""), cmd("mike", "")})
	all := r.All()
	if got := names(all); got != "alpha mike zulu" {
		t.Errorf("All() = %q, want sorted", got)
	}
	all[0] = nil // mutating the copy must not affect the registry
	if r.All()[0].Name != "alpha" {
		t.Error("All() slice is not a copy")
	}
}

func TestRegistryReplaceClearsOld(t *testing.T) {
	r := NewRegistry()
	r.Register(cmd("old", ""))
	r.Replace([]*Command{cmd("new", "")})
	if r.Len() != 1 {
		t.Fatalf("Len = %d, want 1", r.Len())
	}
	if _, ok := r.Get("old"); ok {
		t.Error("Replace should clear previous entries")
	}
	if _, ok := r.Get("new"); !ok {
		t.Error("Replace should install new entries")
	}
	r.Replace(nil)
	if r.Len() != 0 {
		t.Fatal("Replace(nil) should empty the registry")
	}
}

func TestRegistryIgnoresNilAndEmpty(t *testing.T) {
	r := NewRegistry()
	r.Register(nil)
	r.Register(&Command{})
	if r.Len() != 0 {
		t.Errorf("nil / empty-name commands stored: Len = %d, want 0", r.Len())
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); r.Register(cmd(fmt.Sprintf("c%d", i), "")) }(i)
		go func() { defer wg.Done(); _ = r.All(); _, _ = r.Get("c1"); _ = r.Len() }()
	}
	wg.Wait()
	if r.Len() != 20 {
		t.Errorf("Len = %d, want 20", r.Len())
	}
}
