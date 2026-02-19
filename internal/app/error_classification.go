package app

type classifiedError struct {
	Category  string
	Retryable bool
}

var errorCodeClasses = map[string]classifiedError{
	"usage_error":            {Category: "usage", Retryable: false},
	"validation_error":       {Category: "usage", Retryable: false},
	"config_missing":         {Category: "config", Retryable: false},
	"config_error":           {Category: "config", Retryable: false},
	"state_error":            {Category: "runtime", Retryable: false},
	"state_save_failed":      {Category: "runtime", Retryable: false},
	"auth_missing":           {Category: "auth", Retryable: false},
	"not_found":              {Category: "not_found", Retryable: false},
	"idempotency_conflict":   {Category: "conflict", Retryable: false},
	"confirmation_required":  {Category: "safety", Retryable: false},
	"safety_blocked":         {Category: "safety", Retryable: false},
	"doctor_prereq_failed":   {Category: "config", Retryable: false},
	"rate_limit":             {Category: "rate_limit", Retryable: true},
	"bridge_unreachable":     {Category: "transient", Retryable: true},
	"send_failed":            {Category: "transient", Retryable: true},
	"imap_connect_failed":    {Category: "transient", Retryable: true},
	"imap_search_failed":     {Category: "transient", Retryable: true},
	"imap_list_failed":       {Category: "transient", Retryable: true},
	"imap_tag_update_failed": {Category: "transient", Retryable: true},
	"imap_draft_create_failed": {
		Category:  "transient",
		Retryable: true,
	},
}

func classifyCLIError(code string, exit int) classifiedError {
	if c, ok := errorCodeClasses[code]; ok {
		return c
	}
	switch exit {
	case 2:
		return classifiedError{Category: "usage", Retryable: false}
	case 3:
		return classifiedError{Category: "config", Retryable: false}
	case 4:
		return classifiedError{Category: "transient", Retryable: true}
	case 5:
		return classifiedError{Category: "not_found", Retryable: false}
	case 6:
		return classifiedError{Category: "conflict", Retryable: false}
	case 7:
		return classifiedError{Category: "safety", Retryable: false}
	case 8:
		return classifiedError{Category: "rate_limit", Retryable: true}
	default:
		return classifiedError{Category: "runtime", Retryable: false}
	}
}
