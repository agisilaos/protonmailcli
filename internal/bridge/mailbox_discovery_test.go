package bridge

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestDiscoveryCountsEveryPossibleLegacyDraftMailbox(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rows       []string
		selected   string
		candidates []string
	}{
		{"literal selected role", []string{`* LIST (\Drafts) "/" Drafts`, `* LIST (\Drafts) "/" Brouillons`}, "Drafts", []string{"Brouillons", "Drafts"}},
		{"two roles", []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST (\Drafts) "/" "Brouillons"`}, "Entwuerfe", []string{"Brouillons", "Entwuerfe"}},
		{"case variant", []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" DRAFTS`}, "Entwuerfe", []string{"DRAFTS", "Entwuerfe"}},
		{"exact fallback spelling", []string{`* LIST () "/" DRAFTS`}, "DRAFTS", []string{"DRAFTS"}},
		{"same role repeated", []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST (\Drafts) "/" "Entwuerfe"`}, "Entwuerfe", []string{"Entwuerfe"}},
		{"selectable advertised role", []string{`* LIST (\Drafts \Noselect) NIL Brouillons`, `* LIST () NIL Drafts`, `* LIST (\Drafts) NIL Entwuerfe`}, "Entwuerfe", []string{"Brouillons", "Drafts", "Entwuerfe"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got DraftMailbox
			err, _ := runIMAPExchange(t, func(c *IMAPClient) (err error) {
				got, err = c.ResolveDraftMailbox()
				return err
			}, func(tag, _ string) string {
				return strings.Join(tc.rows, "\r\n") + "\r\n" + tag + " OK listed\r\n"
			})
			if err != nil || got.Name != tc.selected || got.AmbiguousLegacyID != (len(tc.candidates) > 1) || !slices.Equal(got.LegacyCandidates, tc.candidates) {
				t.Fatalf("got %+v err=%v, want %q candidates=%v", got, err, tc.selected, tc.candidates)
			}
		})
	}
}

func TestDiscoveryRejectsUnavailableRoleInsteadOfOrdinaryDrafts(t *testing.T) {
	for _, rows := range []string{
		"* LIST (\\Drafts \\Noselect) NIL Brouillons\r\n* LIST () NIL Drafts\r\n",
		"* LIST (\\Drafts) NIL Brouillons\r\n* LIST (\\Noselect) NIL Brouillons\r\n* LIST () NIL Drafts\r\n",
		"* LIST (\\Drafts \\Noselect) NIL Brouillons\r\n* LIST (\\drafts \\noselect) NIL Entwuerfe\r\n* LIST () NIL DRAFTS\r\n",
	} {
		for _, method := range []string{"append", "preview"} {
			t.Run(method+"/"+rows, func(t *testing.T) {
				err, commands := runIMAPExchange(t, func(c *IMAPClient) error {
					if method == "preview" {
						return c.ExamineDraftDestination()
					}
					_, err := c.AppendDraft("Subject: fixture\r\n\r\nfixture")
					return err
				}, func(tag, command string) string {
					if command == `LIST "" "*"` {
						return rows + tag + " OK listed\r\n"
					}
					return ""
				})
				if err == nil || !strings.Contains(err.Error(), "no selectable") || len(commands) != 1 {
					t.Fatalf("unavailable role fell back to an ordinary folder: err=%v commands=%v", err, commands)
				}
			})
		}
	}
}

func TestDraftDiscoveryRejectsAmbiguousImplicitDestination(t *testing.T) {
	for _, rows := range []string{
		"* LIST () NIL DRAFTS\r\n* LIST () NIL drafts\r\n",
	} {
		err, commands := runIMAPExchange(t, func(c *IMAPClient) error {
			_, err := c.AppendDraft("Subject: fixture\r\n\r\nfixture")
			return err
		}, func(tag, _ string) string {
			return rows + tag + " OK listed\r\n"
		})
		if err == nil || !strings.Contains(err.Error(), "ambiguous") || len(commands) != 1 {
			t.Fatalf("err=%v commands=%v", err, commands)
		}
	}
}

func TestDiscoveryIncludesLiteralMailboxNames(t *testing.T) {
	row := "* LIST (\\Drafts) \"/\" Entwuerfe\r\n* LIST () \"/\" {6}\r\nDrafts\r\n"
	var boxes []Mailbox
	var got DraftMailbox
	err, _ := runIMAPExchange(t, func(c *IMAPClient) (err error) {
		boxes, err = c.ListMailboxes()
		if err != nil {
			return err
		}
		got, err = c.ResolveDraftMailbox()
		return err
	}, func(tag, _ string) string { return row + tag + " OK listed\r\n" })
	if err != nil || len(boxes) != 2 || boxes[0].Name != "Drafts" || !got.AmbiguousLegacyID || !slices.Equal(got.LegacyCandidates, []string{"Drafts", "Entwuerfe"}) {
		t.Fatalf("literal mailbox lost: boxes=%+v draft=%+v err=%v", boxes, got, err)
	}
}

func TestDiscoveryPreservesLiteralRoleName(t *testing.T) {
	name := `Dossiers: "Projet" ` // The trailing space is part of the server name.
	var got string
	err, _ := runIMAPExchange(t, func(c *IMAPClient) (err error) {
		got, err = c.DraftMailboxName()
		return err
	}, func(tag, _ string) string {
		return fmt.Sprintf("* LIST (\\Drafts) NIL {%d}\r\n%s\r\n%s OK listed\r\n", len(name), name, tag)
	})
	if err != nil || got != name {
		t.Fatalf("literal role name=%q err=%v", got, err)
	}
}

func TestDiscoveryFailsOnUnreadableListRow(t *testing.T) {
	for _, method := range []string{"list", "resolve"} {
		err, _ := runIMAPExchange(t, func(c *IMAPClient) error {
			if method == "list" {
				_, err := c.ListMailboxes()
				return err
			}
			_, err := c.ResolveDraftMailbox()
			return err
		}, func(tag, _ string) string {
			return "* LIST (\\Drafts) NIL Entwuerfe\r\n* LIST () NIL {invalid}\r\n" + tag + " OK listed\r\n"
		})
		if err == nil {
			t.Errorf("%s accepted a partial listing", method)
		}
	}
}

func TestListMailboxesMergesFlagsForExactNames(t *testing.T) {
	var boxes []Mailbox
	err, _ := runIMAPExchange(t, func(c *IMAPClient) (err error) {
		boxes, err = c.ListMailboxes()
		return err
	}, func(tag, _ string) string {
		return "* LIST (\\HasNoChildren) NIL Entwuerfe\r\n" +
			"* LIST (\\Drafts \\hasnochildren) NIL \"Entwuerfe\"\r\n" +
			"* LIST () NIL entwuerfe\r\n" + tag + " OK listed\r\n"
	})
	if err != nil || len(boxes) != 2 || boxes[0].Name != "Entwuerfe" || boxes[1].Name != "entwuerfe" ||
		!slices.Equal(boxes[0].Flags, []string{`\HasNoChildren`, `\Drafts`}) {
		t.Fatalf("listed mailboxes=%+v err=%v", boxes, err)
	}
}
