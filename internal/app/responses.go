package app

type mailboxInfo struct {
	Name string `json:"name"`
}

type mailboxListResponse struct {
	Mailboxes []mailboxInfo `json:"mailboxes"`
	Count     int           `json:"count"`
}

type draftRecord struct {
	ID      string   `json:"id"`
	UID     string   `json:"uid"`
	To      []string `json:"to,omitempty"`
	From    string   `json:"from,omitempty"`
	Subject string   `json:"subject,omitempty"`
	Body    string   `json:"body,omitempty"`
	Date    string   `json:"date,omitempty"`
	Flags   []string `json:"flags,omitempty"`
}

type draftListResponse struct {
	Drafts     []draftRecord `json:"drafts"`
	Count      int           `json:"count"`
	Total      int           `json:"total"`
	NextCursor string        `json:"nextCursor,omitempty"`
	Source     string        `json:"source"`
}

type draftResponse struct {
	Draft      draftRecord `json:"draft"`
	CreatePath string      `json:"createPath,omitempty"`
	Source     string      `json:"source"`
}

type messageRecord struct {
	ID      string   `json:"id"`
	UID     string   `json:"uid"`
	From    string   `json:"from,omitempty"`
	To      []string `json:"to,omitempty"`
	Subject string   `json:"subject,omitempty"`
	Body    string   `json:"body,omitempty"`
	Flags   []string `json:"flags,omitempty"`
	Date    string   `json:"date,omitempty"`
}

type messageGetResponse struct {
	Message messageRecord `json:"message"`
	Source  string        `json:"source"`
}

type messageListResponse struct {
	Messages   []messageRecord `json:"messages"`
	Count      int             `json:"count"`
	Total      int             `json:"total"`
	NextCursor string          `json:"nextCursor,omitempty"`
	Mailbox    string          `json:"mailbox,omitempty"`
	Source     string          `json:"source"`
}

type batchItemResponse struct {
	Index      int      `json:"index"`
	OK         bool     `json:"ok"`
	DryRun     bool     `json:"dryRun,omitempty"`
	To         []string `json:"to,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	DraftID    string   `json:"draftId,omitempty"`
	UID        string   `json:"uid,omitempty"`
	CreatePath string   `json:"createPath,omitempty"`
	SendPath   string   `json:"sendPath,omitempty"`
	SentAt     string   `json:"sentAt,omitempty"`
	ErrorCode  string   `json:"errorCode,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type batchResultResponse struct {
	Results  []batchItemResponse `json:"results"`
	Count    int                 `json:"count"`
	Success  int                 `json:"success"`
	Failed   int                 `json:"failed"`
	Source   string              `json:"source"`
	exitCode int
}

func (r batchResultResponse) ExitCode() int {
	return r.exitCode
}
