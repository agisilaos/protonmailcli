package app

import (
	"bytes"
	"os"
	"testing"
)

func TestFreshTagAndFilterPreviewsReturnProposedProperties(t *testing.T) {
	for _, mode := range []string{"json", "plain", "human"} {
		for _, kind := range []string{"tag", "filter"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				c := newRegressionCLI(t)
				args := []string{kind, "create", "--name", "finance"}
				if kind == "filter" {
					args = append(args, "--contains", "invoice", "--add-tag", "review")
				}
				plan := localPreviewData(t, c, mode, "", append([]string{"--dry-run"}, args...)...)
				// Preserve jq '.data.tag.name' and '.data.filter.contains'.
				proposed, ok := plan[kind].(map[string]any)
				if plan["action"] != kind+".create" || plan["wouldCreate"] != true || plan["source"] != "local" || !ok || proposed["name"] != "finance" ||
					(kind == "filter" && (proposed["contains"] != "invoice" || proposed["addTag"] != "review")) || (kind == "tag" && plan["changed"] != true) {
					t.Fatalf("preview lost proposed properties or returned a receipt: %+v", plan)
				}
				for _, key := range []string{"id", "createdAt"} {
					if _, exists := proposed[key]; exists {
						t.Fatalf("fresh preview fabricated receipt field %s: %+v", key, plan)
					}
				}
				for _, key := range []string{"name", "contains", "addTag"} {
					if _, exists := plan[key]; exists {
						t.Fatalf("preview duplicated the nested %s property: %+v", key, plan)
					}
				}
				if _, err := os.Stat(c.state); !os.IsNotExist(err) {
					t.Fatalf("preview wrote state: %v", err)
				}
				created := localPreviewData(t, c, mode, "", args...)[kind].(map[string]any)
				if created["id"] == "" || created["id"] == nil || created["name"] != proposed["name"] {
					t.Fatalf("actual creation omitted a saved identity or changed the name: %+v", created)
				}
				if kind == "filter" {
					if created["contains"] != proposed["contains"] || created["addTag"] != proposed["addTag"] {
						t.Fatalf("filter creation differs from preview: plan=%+v saved=%+v", plan, created)
					}
					localPreviewData(t, c, mode, "", "filter", "test", "--filter-id", created["id"].(string))
				}
			})
		}
	}
}

func TestExistingTagPreviewRetainsStoredIdentity(t *testing.T) {
	for _, mode := range []string{"json", "plain", "human"} {
		t.Run(mode, func(t *testing.T) {
			c := newRegressionCLI(t)
			created := localPreviewData(t, c, mode, "", "tag", "create", "--name", "finance")["tag"].(map[string]any)
			before, err := os.ReadFile(c.state)
			if err != nil {
				t.Fatal(err)
			}
			preview := localPreviewData(t, c, mode, "", "--dry-run", "tag", "create", "--name", "finance")
			tag, ok := preview["tag"].(map[string]any)
			if !ok || tag["id"] != created["id"] || tag["name"] != "finance" || preview["changed"] != false || preview["wouldCreate"] == true {
				t.Fatalf("existing tag preview lost its saved identity: %+v", preview)
			}
			after, err := os.ReadFile(c.state)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("existing tag preview changed state: %v", err)
			}
		})
	}
}
