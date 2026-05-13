package mcp

import "testing"

func TestLegacyToolInputArrayPropertiesDeclareItems(t *testing.T) {
	t.Setenv(smartToolModeEnv, "legacy")

	s := NewServer()
	for _, tool := range s.tools {
		for name, prop := range tool.InputSchema.Properties {
			if prop.Type != "array" {
				continue
			}
			if prop.Items == nil || prop.Items.Type == "" {
				t.Fatalf("%s input property %s is an array without items: %#v", tool.Name, name, prop)
			}
		}
	}
}
