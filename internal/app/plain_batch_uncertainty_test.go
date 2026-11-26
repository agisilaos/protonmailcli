package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlainSendManyUncertaintyReportsProcessedRows(t *testing.T) {
	base := imapCommandArgs(t, newWorkflowServer(t, &workflowServer{}))
	delivered := captureWorkflowSMTPWithReplies(t, base, "250 accepted", "")
	args := append(base, "--plain", "message", "send-many", "--stdin")
	input := `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42"},{"draft_id":"43","confirm_send":"43"}]`
	code, out := runWorkflow(t, args, input)
	fields := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if code != 4 || len(delivered()) != 2 || strings.Count(out, "\n") != 1 || len(fields) != 10 ||
		fields[3] != "imap_send_uncertain" || fields[8] != "data" {
		t.Fatalf("missing uncertainty record: exit=%d out=%q", code, out)
	}
	var data batchResultResponse
	if err := json.Unmarshal([]byte(fields[9]), &data); err != nil {
		t.Fatal(err)
	}
	if data.Count != 2 || data.Success != 1 || data.Failed != 1 || len(data.Results) != 2 ||
		data.Results[0].DraftID != "imap-v2:Drafts:41" || !data.Results[0].OK ||
		data.Results[1].DraftID != "imap-v2:Drafts:42" || data.Results[1].ErrorCode != "imap_send_uncertain" {
		t.Fatalf("cannot reconcile the processed rows: %+v", data)
	}
}
