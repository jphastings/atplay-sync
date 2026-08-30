// internal/discord/members_test.go
package discord

import "testing"

func TestMemberCache_SetAndUsername(t *testing.T) {
	c := NewMemberCache()
	c.Set("690973862245957683", "jphastings")
	got, ok := c.Username("690973862245957683")
	if !ok || got != "jphastings" {
		t.Fatalf("Username = %q, %v, want jphastings, true", got, ok)
	}
}

func TestMemberCache_Remove(t *testing.T) {
	c := NewMemberCache()
	c.Set("1", "alice")
	c.Remove("1")
	if _, ok := c.Username("1"); ok {
		t.Fatal("Username after Remove = true, want false")
	}
}

func TestMemberCache_FindByUsername_CaseInsensitive(t *testing.T) {
	c := NewMemberCache()
	c.Set("690973862245957683", "jphastings")
	id, ok := c.FindByUsername("JPHastings")
	if !ok || id != "690973862245957683" {
		t.Fatalf("FindByUsername = %q, %v, want the matching ID", id, ok)
	}
}

func TestMemberCache_FindByUsername_NoMatch(t *testing.T) {
	c := NewMemberCache()
	if _, ok := c.FindByUsername("nobody"); ok {
		t.Fatal("FindByUsername(nobody) = true, want false")
	}
}
