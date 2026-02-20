package app

import "protonmailcli/internal/model"

type mailboxInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Count int    `json:"count,omitempty"`
}

type mailboxListResponse struct {
	Mailboxes []mailboxInfo `json:"mailboxes"`
	Count     int           `json:"count"`
}

type mailboxResolveResponse struct {
	Mailbox   mailboxInfo `json:"mailbox"`
	MatchedBy string      `json:"matchedBy"`
	Source    string      `json:"source"`
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

type localDraftResponse struct {
	Draft      model.Draft `json:"draft"`
	CreatePath string      `json:"createPath,omitempty"`
	Source     string      `json:"source,omitempty"`
}

type localDraftListResponse struct {
	Drafts []model.Draft `json:"drafts"`
	Count  int           `json:"count"`
}

type draftDeleteResponse struct {
	Deleted bool   `json:"deleted"`
	DraftID string `json:"draftId"`
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

type localMessageGetResponse struct {
	Message model.Message `json:"message"`
}

type messageSendResponse struct {
	Sent     bool          `json:"sent"`
	Message  model.Message `json:"message"`
	SendPath string        `json:"sendPath,omitempty"`
	Source   string        `json:"source,omitempty"`
}

type sendPlanResponse struct {
	Action    string `json:"action"`
	DraftID   string `json:"draftId"`
	WouldSend bool   `json:"wouldSend"`
	DryRun    bool   `json:"dryRun"`
	SendPath  string `json:"sendPath,omitempty"`
	Source    string `json:"source,omitempty"`
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

type setupResponse struct {
	Configured bool   `json:"configured"`
	ConfigPath string `json:"configPath"`
}

type authStatusResponse struct {
	LoggedIn     bool   `json:"loggedIn"`
	Username     string `json:"username,omitempty"`
	PasswordFile string `json:"passwordFile,omitempty"`
}

type authLoginResponse struct {
	LoggedIn bool   `json:"loggedIn"`
	Username string `json:"username,omitempty"`
}

type bridgeAccountItem struct {
	Username string `json:"username"`
	Active   bool   `json:"active"`
}

type bridgeAccountListResponse struct {
	Accounts []bridgeAccountItem `json:"accounts"`
	Count    int                 `json:"count"`
	Active   string              `json:"active,omitempty"`
}

type bridgeAccountUseResponse struct {
	Active struct {
		Username string `json:"username"`
	} `json:"active"`
	Changed bool `json:"changed"`
}

type localSearchDraftsResponse struct {
	Drafts []model.Draft `json:"drafts"`
	Count  int           `json:"count"`
}

type localSearchMessagesResponse struct {
	Messages []model.Message `json:"messages"`
	Count    int             `json:"count"`
}

type tagInfo struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
}

type tagListResponse struct {
	Tags   []string `json:"tags"`
	Count  int      `json:"count"`
	Source string   `json:"source,omitempty"`
}

type tagCreateResponse struct {
	Tag     tagInfo `json:"tag"`
	Changed bool    `json:"changed"`
	Source  string  `json:"source,omitempty"`
}

type tagUpdateResponse struct {
	MessageID string `json:"messageId"`
	Tag       string `json:"tag"`
	Changed   bool   `json:"changed"`
	Source    string `json:"source,omitempty"`
}

type filterListResponse struct {
	Filters []model.Filter `json:"filters"`
	Count   int            `json:"count"`
}

type filterCreateResponse struct {
	Filter model.Filter `json:"filter"`
}

type filterDeleteResponse struct {
	Deleted  bool   `json:"deleted"`
	FilterID string `json:"filterId"`
}

type filterApplyResponse struct {
	FilterID string `json:"filterId"`
	Mode     string `json:"mode"`
	Matched  int    `json:"matched"`
	Changed  int    `json:"changed"`
}
